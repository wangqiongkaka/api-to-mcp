package platform

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

func TestMetricsFullDayDropLTTBAndChangePoints(t *testing.T) {
	// 复现 PPT 场景：全天 1440 个点（1 分钟步长），在 2026-05-21 08:00:00+08:00 发生骤降（83.92 -> 43.95）
	start := time.Date(2026, 5, 21, 0, 0, 0, 0, metricsTimeZone)
	times := make([]any, 1440)
	vals := make([]any, 1440)
	for i := 0; i < 1440; i++ {
		ts := start.Add(time.Duration(i) * time.Minute)
		times[i] = ts.UnixMilli()
		switch {
		case i < 479:
			vals[i] = 84.0 + math.Sin(float64(i)/30.0)*0.4
		case i == 479: // 07:59
			vals[i] = 83.92
		case i == 480: // 08:00 骤降
			vals[i] = 43.95
		default:
			vals[i] = 43.10 + math.Sin(float64(i)/30.0)*0.3
		}
	}
	// 在第 900 分钟（15:00）埋入一个单点尖峰，验证 LTTB 保形与全量 stats 精度不受降采样影响
	vals[900] = 95.5

	raw, err := json.Marshal(map[string]any{
		"results": map[string]any{
			"A": map[string]any{
				"status": 200,
				"frames": []map[string]any{
					{
						"schema": map[string]any{
							"name":  "cpu_usage",
							"refId": "A",
							"fields": []map[string]any{
								{"name": "Time", "type": "time"},
								{"name": "Value", "type": "number", "labels": map[string]string{"instance": "node-1"}},
							},
						},
						"data": map[string]any{
							"values": [][]any{times, vals},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := formatGrafanaResponse("2026-05-21T00:00:00+08:00", "2026-05-21T23:59:00+08:00", []GrafanaQuery{
		{RefID: "A", DatasourceType: "prometheus", DataType: "metrics"},
	}, raw)
	if err != nil {
		t.Fatalf("格式化全天指标失败：%v", err)
	}

	var parsed struct {
		Results map[string]struct {
			DataType string `json:"dataType"`
			Frames   []struct {
				Name          string               `json:"name"`
				Labels        map[string]string    `json:"labels"`
				TotalPoints   int                  `json:"totalPoints"`
				SampledPoints int                  `json:"sampledPoints"`
				Stats         MetricsStats         `json:"stats"`
				ChangePoints  []MetricsChangePoint `json:"changePoints"`
				Series        [][2]any             `json:"series"`
			} `json:"frames"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	frame := parsed.Results["A"].Frames[0]
	if frame.TotalPoints != 1440 || frame.SampledPoints != 240 || len(frame.Series) != 240 {
		t.Fatalf("LTTB 默认降采样点数不符：total=%d sampled=%d len=%d", frame.TotalPoints, frame.SampledPoints, len(frame.Series))
	}
	// 全量统计必须命中真实单点尖峰 95.5 及精确东八区时间 15:00
	if frame.Stats.Count != 1440 || frame.Stats.Max != 95.5 || frame.Stats.MaxTime != "2026-05-21T15:00:00+08:00" {
		t.Fatalf("全量统计量不准确：%+v", frame.Stats)
	}
	// 首末点必须严格保留
	if frame.Series[0][0] != "2026-05-21T00:00:00+08:00" || frame.Series[239][0] != "2026-05-21T23:59:00+08:00" {
		t.Fatalf("首末点未保留：first=%v last=%v", frame.Series[0], frame.Series[239])
	}
	// LTTB 必须保住 15:00 尖峰点与 08:00 骤降点
	sawSpike, sawDrop := false, false
	for _, pt := range frame.Series {
		if pt[0] == "2026-05-21T15:00:00+08:00" && pt[1] == 95.5 {
			sawSpike = true
		}
		if pt[0] == "2026-05-21T08:00:00+08:00" && pt[1] == 43.95 {
			sawDrop = true
		}
	}
	if !sawSpike || !sawDrop {
		t.Fatalf("LTTB 未保住关键转折点：sawSpike=%v sawDrop=%v", sawSpike, sawDrop)
	}
	// 突变点检测（按 |delta| 降序）：15:00 尖峰上下跳变约 ±52，08:00 骤降 -39.97 均应被精确检出
	if len(frame.ChangePoints) != 3 {
		t.Fatalf("应检出 3 个突变点，实际 %d：%+v", len(frame.ChangePoints), frame.ChangePoints)
	}
	found0800 := false
	for _, cp := range frame.ChangePoints {
		if cp.FromTime == "2026-05-21T07:59:00+08:00" && cp.ToTime == "2026-05-21T08:00:00+08:00" && math.Abs(cp.Delta-(-39.97)) < 1e-6 {
			found0800 = true
		}
	}
	if !found0800 {
		t.Fatalf("未精确检出 08:00 骤降突变点：%+v", frame.ChangePoints)
	}
}

func TestMetricsCustomParamsAndEdgeCases(t *testing.T) {
	// 1. 常量平坦序列：极差为 0，不应产生任何突变点
	flatPts := make([]tsPoint, 20)
	for i := range flatPts {
		flatPts[i] = tsPoint{tsMs: 1779321600000 + int64(i)*60000, val: 50.0}
	}
	if cps := detectChangePoints(flatPts, 50.0, 50.0, 0.3, 5); len(cps) != 0 {
		t.Fatalf("平坦序列不应出现突变点：%+v", cps)
	}

	// 2. 自定义 changePointRatio 与 maxChangePoints 截断（按 |delta| 降序）
	zigzag := []tsPoint{
		{tsMs: 1779321600000, val: 10},
		{tsMs: 1779321660000, val: 40},  // +30
		{tsMs: 1779321720000, val: 0},   // -40
		{tsMs: 1779321780000, val: 100}, // +100
		{tsMs: 1779321840000, val: 20},  // -80
		{tsMs: 1779321900000, val: 70},  // +50
	}
	top2 := detectChangePoints(zigzag, 0, 100, 0.3, 2)
	if len(top2) != 2 || top2[0].Delta != 100 || top2[1].Delta != -80 {
		t.Fatalf("突变点按幅度截断不符：%+v", top2)
	}
	zero := detectChangePoints(zigzag, 0, 100, 0.3, 0)
	if len(zero) != 0 {
		t.Fatalf("maxChangePoints=0 时应返回空：%+v", zero)
	}

	// 3. 多数值列 + null 空洞 + 乱序时间戳 + 非时序表格帧回退 rows
	raw := []byte(`{
		"results": {
			"M": {
				"status": 200,
				"frames": [{
					"schema": {
						"name": "host_metrics",
						"fields": [
							{"name": "Time", "type": "time"},
							{"name": "cpu", "type": "number", "labels": {"metric": "cpu"}},
							{"name": "mem", "type": "number", "labels": {"metric": "mem"}}
						]
					},
					"data": {
						"values": [
							[1779321720000, 1779321600000, 1779321660000],
							[30, 10, null],
							[80, 20, 50]
						]
					}
				}]
			},
			"T": {
				"status": 200,
				"frames": [{
					"schema": {
						"name": "table_result",
						"fields": [
							{"name": "Time", "type": "time"},
							{"name": "Host", "type": "string"},
							{"name": "Count", "type": "number"}
						]
					},
					"data": {
						"values": [
							[1779321600000],
							["node-a"],
							[42]
						]
					}
				}]
			}
		}
	}`)
	queries := []GrafanaQuery{
		{RefID: "M", DatasourceType: "prometheus", DataType: "metrics", MaxPoints: 2, ChangePointRatio: floatPtr(0.5), MaxChangePoints: intPtr(1)},
		{RefID: "T", DatasourceType: "grafana-clickhouse-datasource", DataType: "metrics"},
	}
	out, err := formatGrafanaResponse("now-1h", "now", queries, raw)
	if err != nil {
		t.Fatalf("多列与表格帧格式化失败：%v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	mFrames := parsed["results"].(map[string]any)["M"].(map[string]any)["frames"].([]any)
	if len(mFrames) != 2 {
		t.Fatalf("2 个数值列应拆分为 2 条独立时序，实际 %d", len(mFrames))
	}
	cpuFrame := mFrames[0].(map[string]any)
	if cpuFrame["name"] != "host_metrics.cpu" || cpuFrame["totalPoints"] != float64(2) {
		t.Fatalf("cpu 序列过滤 null 与命名不符：%#v", cpuFrame)
	}
	cpuSeries := cpuFrame["series"].([]any)
	// 乱序输入应已按时间升序排列
	if cpuSeries[0].([]any)[0] != "2026-05-21T08:00:00+08:00" || cpuSeries[1].([]any)[0] != "2026-05-21T08:02:00+08:00" {
		t.Fatalf("乱序时间戳未正确排序：%#v", cpuSeries)
	}
	memFrame := mFrames[1].(map[string]any)
	if memFrame["totalPoints"] != float64(3) || memFrame["sampledPoints"] != float64(2) {
		t.Fatalf("mem 序列 MaxPoints=2 未生效：%#v", memFrame)
	}
	// 含字符串维度列的表格帧应回退保留 rows，不丢失 Host 列
	tFrame := parsed["results"].(map[string]any)["T"].(map[string]any)["frames"].([]any)[0].(map[string]any)
	if _, hasRows := tFrame["rows"]; !hasRows {
		t.Fatalf("含字符串列的表格帧应保留 rows：%#v", tFrame)
	}
	if _, hasSeries := tFrame["series"]; hasSeries {
		t.Fatalf("含字符串列的表格帧不应输出 series：%#v", tFrame)
	}
}

func TestValidateMetricsAnalysisConfig(t *testing.T) {
	baseDef := func(dt string, mutate func(*GrafanaQuery)) Definition {
		q := GrafanaQuery{
			RefID:          "A",
			DatasourceUID:  "prom-1",
			DatasourceType: "prometheus",
			DataType:       dt,
			Expr:           "up",
		}
		mutate(&q)
		return Definition{
			Kind:           "grafana",
			Name:           "metrics_cfg_test",
			Description:    "测试时序分析参数校验",
			TimeoutSeconds: 15,
			Grafana: &GrafanaConfig{
				ConnectionID: 1,
				DefaultFrom:  "now-1h",
				DefaultTo:    "now",
				Queries:      []GrafanaQuery{q},
			},
		}
	}

	if err := baseDef("metrics", func(q *GrafanaQuery) {
		q.MaxPoints = 120
		q.ChangePointRatio = floatPtr(0.25)
		q.MaxChangePoints = intPtr(3)
	}).Validate(); err != nil {
		t.Fatalf("合法时序分析配置应通过校验：%v", err)
	}
	if err := baseDef("metrics", func(q *GrafanaQuery) {
		q.MaxChangePoints = intPtr(0)
	}).Validate(); err != nil {
		t.Fatalf("maxChangePoints=0 应允许（关闭突变点报告）：%v", err)
	}

	invalid := []struct {
		name string
		def  Definition
	}{
		{"logs 不允许配置时序参数", baseDef("logs", func(q *GrafanaQuery) { q.MaxPoints = 100 })},
		{"maxPoints 过小", baseDef("metrics", func(q *GrafanaQuery) { q.MaxPoints = 1 })},
		{"maxPoints 过大", baseDef("metrics", func(q *GrafanaQuery) { q.MaxPoints = 10001 })},
		{"changePointRatio 为 0", baseDef("metrics", func(q *GrafanaQuery) { q.ChangePointRatio = floatPtr(0) })},
		{"changePointRatio 超过 1", baseDef("metrics", func(q *GrafanaQuery) { q.ChangePointRatio = floatPtr(1.2) })},
		{"maxChangePoints 为负", baseDef("metrics", func(q *GrafanaQuery) { q.MaxChangePoints = intPtr(-1) })},
		{"maxChangePoints 过大", baseDef("metrics", func(q *GrafanaQuery) { q.MaxChangePoints = intPtr(101) })},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.def.Validate(); err == nil {
				t.Fatalf("非法场景 %s 应校验失败", tc.name)
			}
		})
	}
}
