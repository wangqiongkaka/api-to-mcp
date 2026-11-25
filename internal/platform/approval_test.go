package platform

import "testing"

func TestApprovalOutcomeStatus(t *testing.T) {
	for want, outcome := range map[string]approvalOutcome{
		"failed":         {Error: "接口已停用，未执行"},
		"unknown":        {Sent: true, Error: "请求已发往上游，结果未知"},
		"upstream_error": {Sent: true, Result: &CallResult{StatusCode: 502}},
		"executed":       {Sent: true, Result: &CallResult{StatusCode: 201}},
	} {
		if got := outcome.status(); got != want {
			t.Errorf("%+v 的状态为 %s，预期 %s", outcome, got, want)
		}
	}
}
