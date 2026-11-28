package platform

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	defaultMetricsMaxPoints        = 240
	minMetricsMaxPoints            = 2
	maxMetricsMaxPoints            = 10000
	defaultMetricsChangePointRatio = 0.3
	defaultMetricsMaxChangePoints  = 5
	maxMetricsMaxChangePoints      = 100
)

// 使用固定东八区偏移，避免依赖容器镜像内置 tzdata 且让 LLM 无需心算 UTC+8。
var metricsTimeZone = time.FixedZone("CST", 8*3600)

type MetricsStats struct {
	Count     int     `json:"count"`
	Min       float64 `json:"min"`
	MinTime   string  `json:"minTime"`
	Max       float64 `json:"max"`
	MaxTime   string  `json:"maxTime"`
	Avg       float64 `json:"avg"`
	First     float64 `json:"first"`
	FirstTime string  `json:"firstTime"`
	Last      float64 `json:"last"`
	LastTime  string  `json:"lastTime"`
}

type MetricsChangePoint struct {
	FromTime  string  `json:"fromTime"`
	ToTime    string  `json:"toTime"`
	FromValue float64 `json:"fromValue"`
	ToValue   float64 `json:"toValue"`
	Delta     float64 `json:"delta"`
}

type tsPoint struct {
	tsMs int64
	val  float64
}

func (q GrafanaQuery) resolvedMetricsParams() (maxPoints int, changePointRatio float64, maxChangePoints int) {
	maxPoints = q.MaxPoints
	if maxPoints <= 0 {
		maxPoints = defaultMetricsMaxPoints
	}
	changePointRatio = defaultMetricsChangePointRatio
	if q.ChangePointRatio != nil {
		changePointRatio = *q.ChangePointRatio
	}
	maxChangePoints = defaultMetricsMaxChangePoints
	if q.MaxChangePoints != nil {
		maxChangePoints = *q.MaxChangePoints
	}
	return maxPoints, changePointRatio, maxChangePoints
}

func validateMetricsAnalysisConfig(refID string, q GrafanaQuery) error {
	hasConfig := q.MaxPoints != 0 || q.ChangePointRatio != nil || q.MaxChangePoints != nil
	if !hasConfig {
		return nil
	}
	if q.resolvedDataType() != "metrics" {
		return fmt.Errorf("查询 %s 仅 metrics 数据类型支持配置时序降采样与突变点参数", refID)
	}
	if q.MaxPoints != 0 && (q.MaxPoints < minMetricsMaxPoints || q.MaxPoints > maxMetricsMaxPoints) {
		return fmt.Errorf("查询 %s 的 LTTB 目标点数（maxPoints）须在 %d–%d 之间", refID, minMetricsMaxPoints, maxMetricsMaxPoints)
	}
	if q.ChangePointRatio != nil {
		r := *q.ChangePointRatio
		if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 || r > 1 {
			return fmt.Errorf("查询 %s 的突变点阈值比例（changePointRatio）须大于 0 且不超过 1", refID)
		}
	}
	if q.MaxChangePoints != nil {
		m := *q.MaxChangePoints
		if m < 0 || m > maxMetricsMaxChangePoints {
			return fmt.Errorf("查询 %s 的最多突变点数（maxChangePoints）须在 0–%d 之间", refID, maxMetricsMaxChangePoints)
		}
	}
	return nil
}

func formatMetricsTimeMs(ms int64) string {
	return time.UnixMilli(ms).In(metricsTimeZone).Format(time.RFC3339Nano)
}

func parseTimeMillis(v any) (int64, bool) {
	if v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case json.Number:
		if ms, err := t.Int64(); err == nil {
			return ms, true
		}
		if f, err := t.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return int64(f), true
		}
	case float64:
		if !math.IsNaN(t) && !math.IsInf(t, 0) {
			return int64(t), true
		}
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return parsed.UnixMilli(), true
		}
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			return parsed.UnixMilli(), true
		}
	}
	return 0, false
}

func parseFiniteFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// detectTimeSeriesColumns 判断 DataFrame 是否为纯数值时序帧（1 个 time 列 + 至少 1 个 number 列，无额外非数值维度列）。
func detectTimeSeriesColumns(frame grafanaFrame) (timeIdx int, numIndices []int, ok bool) {
	timeIdx = -1
	for i, f := range frame.Schema.Fields {
		switch strings.ToLower(strings.TrimSpace(f.Type)) {
		case "time":
			if timeIdx == -1 {
				timeIdx = i
			}
		case "number":
			numIndices = append(numIndices, i)
		default:
			return -1, nil, false
		}
	}
	if timeIdx < 0 || len(numIndices) == 0 {
		return -1, nil, false
	}
	return timeIdx, numIndices, true
}

func buildTimeSeriesItems(frame grafanaFrame, notices []string, q GrafanaQuery) ([]formattedGrafanaItem, bool) {
	timeIdx, numIndices, ok := detectTimeSeriesColumns(frame)
	if !ok {
		return nil, false
	}
	cols := frame.Data.Values
	var timeCol []any
	if timeIdx < len(cols) {
		timeCol = cols[timeIdx]
	}
	maxPoints, ratio, maxCP := q.resolvedMetricsParams()
	items := make([]formattedGrafanaItem, 0, len(numIndices))
	multiSeries := len(numIndices) > 1
	for _, numIdx := range numIndices {
		var valCol []any
		if numIdx < len(cols) {
			valCol = cols[numIdx]
		}
		n := len(timeCol)
		if len(valCol) < n {
			n = len(valCol)
		}
		points := make([]tsPoint, 0, n)
		sorted := true
		var prevTs int64
		for i := 0; i < n; i++ {
			ts, okT := parseTimeMillis(timeCol[i])
			val, okV := parseFiniteFloat(valCol[i])
			if !okT || !okV {
				continue
			}
			if len(points) > 0 && ts < prevTs {
				sorted = false
			}
			prevTs = ts
			points = append(points, tsPoint{tsMs: ts, val: val})
		}
		if !sorted {
			sort.SliceStable(points, func(i, j int) bool {
				return points[i].tsMs < points[j].tsMs
			})
		}

		field := frame.Schema.Fields[numIdx]
		name := frame.Schema.Name
		if multiSeries {
			switch {
			case name != "" && field.Name != "" && name != field.Name:
				name = name + "." + field.Name
			case name == "":
				name = field.Name
			}
		} else if name == "" {
			name = field.Name
		}

		labels := make(map[string]string)
		if multiSeries {
			for k, v := range field.Labels {
				labels[k] = v
			}
		} else {
			for _, f := range frame.Schema.Fields {
				for k, v := range f.Labels {
					labels[k] = v
				}
			}
		}

		item := analyzeTimeSeries(name, labels, notices, points, maxPoints, ratio, maxCP)
		items = append(items, item)
	}
	return items, true
}

func analyzeTimeSeries(
	name string,
	labels map[string]string,
	notices []string,
	points []tsPoint,
	maxPoints int,
	changePointRatio float64,
	maxChangePoints int,
) formattedGrafanaItem {
	total := len(points)
	emptyCP := []MetricsChangePoint{}
	emptySeries := [][2]any{}
	if total == 0 {
		sampled := 0
		item := formattedGrafanaItem{
			Name:          name,
			Notices:       notices,
			TotalPoints:   &total,
			SampledPoints: &sampled,
			ChangePoints:  &emptyCP,
			Series:        &emptySeries,
		}
		if len(labels) > 0 {
			item.Labels = labels
		}
		return item
	}

	stats := computeMetricsStats(points)
	cps := detectChangePoints(points, stats.Min, stats.Max, changePointRatio, maxChangePoints)
	downsampled := downsampleLTTB(points, maxPoints)
	series := make([][2]any, len(downsampled))
	for i, pt := range downsampled {
		series[i] = [2]any{formatMetricsTimeMs(pt.tsMs), pt.val}
	}
	sampled := len(series)
	item := formattedGrafanaItem{
		Name:          name,
		Notices:       notices,
		TotalPoints:   &total,
		SampledPoints: &sampled,
		Stats:         &stats,
		ChangePoints:  &cps,
		Series:        &series,
	}
	if len(labels) > 0 {
		item.Labels = labels
	}
	return item
}

func computeMetricsStats(points []tsPoint) MetricsStats {
	minPt, maxPt := points[0], points[0]
	sum := 0.0
	for _, pt := range points {
		sum += pt.val
		if pt.val < minPt.val {
			minPt = pt
		}
		if pt.val > maxPt.val {
			maxPt = pt
		}
	}
	first := points[0]
	last := points[len(points)-1]
	return MetricsStats{
		Count:     len(points),
		Min:       minPt.val,
		MinTime:   formatMetricsTimeMs(minPt.tsMs),
		Max:       maxPt.val,
		MaxTime:   formatMetricsTimeMs(maxPt.tsMs),
		Avg:       sum / float64(len(points)),
		First:     first.val,
		FirstTime: formatMetricsTimeMs(first.tsMs),
		Last:      last.val,
		LastTime:  formatMetricsTimeMs(last.tsMs),
	}
}

func detectChangePoints(points []tsPoint, minVal, maxVal, ratio float64, maxChangePoints int) []MetricsChangePoint {
	if len(points) < 2 || maxChangePoints <= 0 {
		return []MetricsChangePoint{}
	}
	valueRange := maxVal - minVal
	if valueRange <= 0 {
		return []MetricsChangePoint{}
	}
	threshold := valueRange * ratio
	type candidate struct {
		idx      int
		absDelta float64
		cp       MetricsChangePoint
	}
	var candidates []candidate
	for i := 0; i < len(points)-1; i++ {
		delta := points[i+1].val - points[i].val
		absDelta := math.Abs(delta)
		if absDelta >= threshold {
			candidates = append(candidates, candidate{
				idx:      i,
				absDelta: absDelta,
				cp: MetricsChangePoint{
					FromTime:  formatMetricsTimeMs(points[i].tsMs),
					ToTime:    formatMetricsTimeMs(points[i+1].tsMs),
					FromValue: points[i].val,
					ToValue:   points[i+1].val,
					Delta:     delta,
				},
			})
		}
	}
	if len(candidates) == 0 {
		return []MetricsChangePoint{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].absDelta != candidates[j].absDelta {
			return candidates[i].absDelta > candidates[j].absDelta
		}
		return candidates[i].idx < candidates[j].idx
	})
	if len(candidates) > maxChangePoints {
		candidates = candidates[:maxChangePoints]
	}
	out := make([]MetricsChangePoint, len(candidates))
	for i, c := range candidates {
		out[i] = c.cp
	}
	return out
}

// downsampleLTTB 实现 Largest-Triangle-Three-Buckets 保形降采样：
// 保留首尾点，每桶选出与上一已选点 A、下一桶平均点 C 构成三角形面积最大的候选点 B。
func downsampleLTTB(points []tsPoint, threshold int) []tsPoint {
	n := len(points)
	if n <= threshold || threshold < 2 {
		return points
	}
	if threshold == 2 {
		return []tsPoint{points[0], points[n-1]}
	}
	sampled := make([]tsPoint, 0, threshold)
	sampled = append(sampled, points[0])

	every := float64(n-2) / float64(threshold-2)
	a := 0
	for i := 0; i < threshold-2; i++ {
		avgRangeStart := int(math.Floor(float64(i+1)*every)) + 1
		avgRangeEnd := int(math.Floor(float64(i+2)*every)) + 1
		if avgRangeEnd > n {
			avgRangeEnd = n
		}
		avgCount := avgRangeEnd - avgRangeStart
		var avgX, avgY float64
		for j := avgRangeStart; j < avgRangeEnd; j++ {
			avgX += float64(points[j].tsMs)
			avgY += points[j].val
		}
		if avgCount > 0 {
			avgX /= float64(avgCount)
			avgY /= float64(avgCount)
		}

		rangeOffs := int(math.Floor(float64(i)*every)) + 1
		rangeTo := int(math.Floor(float64(i+1)*every)) + 1
		if rangeTo > n-1 {
			rangeTo = n - 1
		}

		pointAX := float64(points[a].tsMs)
		pointAY := points[a].val
		dxAC := pointAX - avgX
		dyAC := avgY - pointAY

		maxArea := -1.0
		nextA := rangeOffs
		for j := rangeOffs; j < rangeTo; j++ {
			area := math.Abs(dxAC*(points[j].val-pointAY) - (pointAX-float64(points[j].tsMs))*dyAC)
			if area > maxArea {
				maxArea = area
				nextA = j
			}
		}
		sampled = append(sampled, points[nextA])
		a = nextA
	}
	sampled = append(sampled, points[n-1])
	return sampled
}
