package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ApprovalRule 存在即表示该接口的 MCP 调用需要审批。
type ApprovalRule struct {
	// Approver 取值：self（调用者本人）、admin（任一管理员）、user（指定用户）。
	Approver     string `json:"approver"`
	ApproverName string `json:"approverName,omitempty"`
}

const approvalSchema = `
CREATE TABLE IF NOT EXISTS approvals (
 id BIGSERIAL PRIMARY KEY, tool_id BIGINT REFERENCES tools(id) ON DELETE SET NULL, tool_name TEXT NOT NULL,
 requester_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 approver_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
 definition_hash BYTEA NOT NULL, arguments BYTEA NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','rejected','executing','executed','upstream_error','unknown','failed')),
 outcome BYTEA, decided_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL,
 decided_at TIMESTAMPTZ, finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS approvals_requester_idx ON approvals(requester_id);
CREATE INDEX IF NOT EXISTS approvals_approver_idx ON approvals(approver_id);
`

func (r *ApprovalRule) validate() error {
	if r == nil {
		return nil
	}
	switch r.Approver {
	case "self", "admin":
		if r.ApproverName != "" {
			return fmt.Errorf("仅指定用户审批时填写审批人")
		}
	case "user":
		if !usernamePattern.MatchString(r.ApproverName) {
			return fmt.Errorf("请填写审批人的用户名")
		}
	default:
		return fmt.Errorf("审批人类型无效")
	}
	return nil
}

func (r *ApprovalRule) normalize() {
	if r != nil {
		r.ApproverName = strings.ToLower(strings.TrimSpace(r.ApproverName))
	}
}

// checkApprover 确认指定审批人存在且未停用。
func (a *App) checkApprover(ctx context.Context, d Definition) error {
	if d.Approval == nil || d.Approval.Approver != "user" {
		return nil
	}
	var exists bool
	if err := a.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1 AND NOT disabled)", d.Approval.ApproverName).Scan(&exists); err != nil {
		return fmt.Errorf("暂时无法确认审批人，请重试")
	}
	if !exists {
		return fmt.Errorf("审批人 %s 不存在或已停用", d.Approval.ApproverName)
	}
	return nil
}

func definitionHash(d Definition) []byte {
	data, _ := json.Marshal(d)
	sum := sha256.Sum256(data)
	return sum[:]
}

// submitApproval 校验并冻结 MCP 调用参数，生成待审批单，不访问上游。
func (a *App) submitApproval(ctx context.Context, user User, tool ToolRecord, args map[string]any) *mcp.CallToolResult {
	if _, _, _, err := a.prepare(ctx, tool.Definition, args); err != nil {
		return toolError(err.Error())
	}
	var approverID *int64
	approverLabel := "管理员"
	switch tool.Approval.Approver {
	case "self":
		approverID, approverLabel = &user.ID, user.Username
	case "user":
		var id int64
		err := a.db.QueryRow(ctx, "SELECT id FROM users WHERE username=$1 AND NOT disabled", tool.Approval.ApproverName).Scan(&id)
		if err != nil {
			return toolError("审批人不存在或已停用，请联系接口维护者")
		}
		approverID, approverLabel = &id, tool.Approval.ApproverName
	}
	frozen, _ := json.Marshal(args)
	sealed, err := a.seal(frozen)
	if err != nil {
		return toolError("审批单创建失败")
	}
	var id int64
	var expires time.Time
	err = a.db.QueryRow(ctx, `INSERT INTO approvals(tool_id,tool_name,requester_id,approver_id,definition_hash,arguments,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,now()+interval '24 hours') RETURNING id,expires_at`, tool.ID, tool.Name, user.ID, approverID, definitionHash(tool.Definition), sealed).Scan(&id, &expires)
	if err != nil {
		return toolError("审批单创建失败")
	}
	a.notifyApproval(approvalNotice{ID: id, Tool: tool.Name, Requester: user.Username, Approver: approverLabel, ExpiresAt: expires})
	text := fmt.Sprintf("已提交审批 #%d，尚未执行。审批人：%s，有效期至 %s。批准后由平台执行并保存结果，结果在接口工坊「审批」页查看。请勿重复提交同一操作。",
		id, approverLabel, expires.Local().Format("2006-01-02 15:04 MST"))
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, StructuredContent: map[string]any{"approvalId": id, "status": "pending"}}
}

type Approval struct {
	ID         int64      `json:"id"`
	ToolID     *int64     `json:"toolId"`
	ToolName   string     `json:"toolName"`
	Method     string     `json:"method,omitempty"`
	Purpose    string     `json:"description,omitempty"`
	Requester  string     `json:"requester"`
	Approver   string     `json:"approver"`
	Status     string     `json:"status"`
	CanDecide  bool       `json:"canDecide"`
	DecidedBy  string     `json:"decidedBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	DecidedAt  *time.Time `json:"decidedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	Arguments json.RawMessage `json:"arguments,omitempty"`
	Request   *requestPreview `json:"request,omitempty"`
	Result    *CallResult     `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type requestPreview struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body,omitempty"`
}

type approvalOutcome struct {
	Result *CallResult `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
	// Sent 表示请求已发往上游；此后出错时写操作可能已生效。
	Sent bool `json:"-"`
}

// status 区分未执行、上游已执行但结果未知、上游返回错误和成功执行。
func (o approvalOutcome) status() string {
	switch {
	case !o.Sent:
		return "failed"
	case o.Error != "":
		return "unknown"
	case o.Result.StatusCode < 200 || o.Result.StatusCode >= 300:
		return "upstream_error"
	}
	return "executed"
}

// 列表不读取加密的参数与结果，只在详情中读取。
const approvalColumns = `SELECT a.id,a.tool_id,a.tool_name,r.username,COALESCE(p.username,'管理员'),
 CASE WHEN a.status='pending' AND a.expires_at<=now() THEN 'expired' ELSE a.status END,
 COALESCE(a.status='pending' AND a.expires_at>now() AND (a.approver_id=$1 OR (a.approver_id IS NULL AND $2)),false),
 COALESCE(d.username,''),a.created_at,a.expires_at,a.decided_at,a.finished_at,%s
 FROM approvals a JOIN users r ON r.id=a.requester_id LEFT JOIN users p ON p.id=a.approver_id LEFT JOIN users d ON d.id=a.decided_by
 WHERE (a.requester_id=$1 OR a.approver_id=$1 OR (a.approver_id IS NULL AND $2))`

type scanner interface{ Scan(...any) error }

func scanApproval(row scanner) (Approval, []byte, []byte, error) {
	var item Approval
	var args, outcome []byte
	err := row.Scan(&item.ID, &item.ToolID, &item.ToolName, &item.Requester, &item.Approver, &item.Status, &item.CanDecide,
		&item.DecidedBy, &item.CreatedAt, &item.ExpiresAt, &item.DecidedAt, &item.FinishedAt, &args, &outcome)
	return item, args, outcome, err
}

func (a *App) approvals(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	// ponytail: 只返回最近 200 条，需要翻页时再加游标。
	rows, err := a.db.Query(r.Context(), fmt.Sprintf(approvalColumns, "NULL::bytea,NULL::bytea")+" ORDER BY a.id DESC LIMIT 200", u.ID, u.Role == "admin")
	if err != nil {
		fail(w, 500, "读取审批单失败")
		return
	}
	defer rows.Close()
	items := []Approval{}
	for rows.Next() {
		item, _, _, err := scanApproval(rows)
		if err != nil {
			fail(w, 500, "读取审批单失败")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取审批单失败")
		return
	}
	writeJSON(w, 200, items)
}

// approvalDetail 返回冻结参数、执行结果；只有接口管理者能看到最终请求地址与正文。
func (a *App) approvalDetail(ctx context.Context, id int64, u User) (Approval, error) {
	item, args, outcome, err := scanApproval(a.db.QueryRow(ctx, fmt.Sprintf(approvalColumns, "a.arguments,a.outcome")+" AND a.id=$3", u.ID, u.Role == "admin", id))
	if err != nil {
		return item, err
	}
	if item.Arguments, err = a.unseal(args); err != nil {
		return item, err
	}
	if outcome != nil {
		data, err := a.unseal(outcome)
		if err != nil {
			return item, err
		}
		var o approvalOutcome
		if err = json.Unmarshal(data, &o); err != nil {
			return item, err
		}
		item.Result, item.Error = o.Result, o.Error
	}
	if item.ToolID != nil {
		// 方法和用途描述对能看到审批单的人公开；最终请求只给接口维护者。
		if tool, err := a.getTool(ctx, *item.ToolID, User{Role: "admin"}, false); err == nil {
			item.Method, item.Purpose = tool.effectiveMethod(), tool.Description
		}
		if tool, err := a.getTool(ctx, *item.ToolID, u, false); err == nil && tool.manageableBy(u) {
			var values map[string]any
			if json.Unmarshal(item.Arguments, &values) == nil {
				if request, _, _, err := a.prepare(ctx, tool.Definition, values); err == nil {
					preview := &requestPreview{Method: request.Method, URL: request.URL.String()}
					if request.Body != nil {
						var body bytes.Buffer
						body.ReadFrom(request.Body)
						preview.Body = body.String()
					}
					item.Request = preview
				}
			}
		}
	}
	return item, nil
}

func (a *App) approval(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	item, err := a.approvalDetail(r.Context(), id, currentUser(r))
	if err != nil {
		if isNotFound(err) {
			fail(w, 404, "审批单不存在")
		} else {
			fail(w, 500, "读取审批单失败")
		}
		return
	}
	writeJSON(w, 200, item)
}

func (a *App) decide(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var input struct {
		Approve *bool `json:"approve"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Approve == nil {
		fail(w, 400, "请选择批准或拒绝")
		return
	}
	next := "rejected"
	if *input.Approve {
		next = "executing"
	}
	// 状态原子地离开 pending，保证同一审批单最多执行一次。
	var toolID *int64
	var requesterID int64
	var hash, sealed []byte
	err = a.db.QueryRow(r.Context(), `UPDATE approvals SET status=$1,decided_by=$2,decided_at=now(),
 finished_at=CASE WHEN $1='rejected' THEN now() END
 WHERE id=$3 AND status='pending' AND expires_at>now() AND (approver_id=$2 OR (approver_id IS NULL AND $4))
 RETURNING tool_id,requester_id,definition_hash,arguments`, next, u.ID, id, u.Role == "admin").Scan(&toolID, &requesterID, &hash, &sealed)
	if err != nil {
		if !isNotFound(err) {
			fail(w, 500, "审批失败，请重试")
		} else if _, err := a.approvalDetail(r.Context(), id, u); err != nil {
			fail(w, 404, "审批单不存在")
		} else {
			fail(w, 409, "审批单已处理、已过期，或你不是该审批单的审批人")
		}
		return
	}
	if *input.Approve {
		// 审批人关闭页面也不能中断已开始的写请求。
		ctx := context.WithoutCancel(r.Context())
		outcome := a.runApproval(ctx, toolID, requesterID, hash, sealed)
		status := outcome.status()
		data, _ := json.Marshal(outcome)
		stored, err := a.seal(data)
		if err == nil {
			_, err = a.db.Exec(ctx, "UPDATE approvals SET status=$1,outcome=$2,finished_at=now() WHERE id=$3", status, stored, id)
		}
		if err != nil {
			fail(w, 500, "接口已执行，但结果保存失败")
			return
		}
	}
	item, err := a.approvalDetail(r.Context(), id, u)
	if err != nil {
		fail(w, 500, "读取审批单失败")
		return
	}
	writeJSON(w, 200, item)
}

// runApproval 复核接口与申请人状态后，使用冻结参数执行一次上游请求。
func (a *App) runApproval(ctx context.Context, toolID *int64, requesterID int64, hash, sealed []byte) approvalOutcome {
	if toolID == nil {
		return approvalOutcome{Error: "接口已删除，未执行"}
	}
	var disabled bool
	if err := a.db.QueryRow(ctx, "SELECT disabled FROM users WHERE id=$1", requesterID).Scan(&disabled); err != nil || disabled {
		return approvalOutcome{Error: "申请人账户已停用，未执行"}
	}
	tool, err := a.getTool(ctx, *toolID, User{ID: requesterID}, true)
	if err != nil {
		return approvalOutcome{Error: "接口已删除或申请人已无权调用，未执行"}
	}
	if !tool.Published {
		return approvalOutcome{Error: "接口已停用，未执行"}
	}
	if !bytes.Equal(definitionHash(tool.Definition), hash) {
		return approvalOutcome{Error: "接口配置在提交后已修改，未执行，请重新提交审批"}
	}
	data, err := a.unseal(sealed)
	if err != nil {
		return approvalOutcome{Error: "审批参数无法读取，未执行"}
	}
	args, err := tool.decodeArguments(data)
	if err != nil {
		return approvalOutcome{Error: "审批参数无法读取，未执行"}
	}
	if _, _, _, err := a.prepare(ctx, tool.Definition, args); err != nil {
		return approvalOutcome{Error: err.Error() + "，未执行"}
	}
	result, err := a.execute(ctx, tool.Definition, args)
	if err != nil {
		return approvalOutcome{Sent: true, Error: "请求已发往上游，结果未知：" + err.Error() + "。请到上游核实，不要直接重新提交"}
	}
	return approvalOutcome{Sent: true, Result: &result}
}

type approvalNotice struct {
	ID        int64
	Tool      string
	Requester string
	Approver  string
	ExpiresAt time.Time
}

// notifyApproval 推送新审批单；推送失败只记录日志，不影响提交。
// 新增推送平台时在这里追加发送逻辑。消息不含调用参数和上游地址。
func (a *App) notifyApproval(n approvalNotice) {
	if a.feishuWebhook == "" {
		return
	}
	link := "请在接口工坊「审批」页处理。"
	if a.publicURL != "" {
		link = "处理地址：" + a.publicURL + "/#approvals"
	}
	text := fmt.Sprintf("接口工坊有新的审批单 #%d\n工具：%s\n申请人：%s\n审批人：%s\n有效期至：%s\n%s",
		n.ID, n.Tool, n.Requester, n.Approver, n.ExpiresAt.Local().Format("2006-01-02 15:04 MST"), link)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.sendFeishu(ctx, text); err != nil {
			log.Printf("审批单 #%d 飞书通知发送失败：%v", n.ID, err)
		}
	}()
}

func (a *App) sendFeishu(ctx context.Context, text string) error {
	message := map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	if a.feishuSecret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+a.feishuSecret))
		message["timestamp"] = timestamp
		message["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	data, _ := json.Marshal(message)
	request, err := http.NewRequestWithContext(ctx, "POST", a.feishuWebhook, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("Webhook 地址无效")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.notifyClient.Do(request)
	if err != nil {
		// 不输出底层错误，避免日志泄露 Webhook 地址中的令牌。
		return fmt.Errorf("网络请求失败")
	}
	defer response.Body.Close()
	var reply struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	json.NewDecoder(response.Body).Decode(&reply)
	if response.StatusCode != 200 || reply.Code != 0 {
		return fmt.Errorf("HTTP %d，code %d：%s", response.StatusCode, reply.Code, reply.Msg)
	}
	return nil
}
