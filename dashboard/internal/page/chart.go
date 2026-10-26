package page

import (
	"html"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	chartWidth  = 640.0
	chartHeight = 220.0
	chartPadL   = 64.0
	chartPadR   = 16.0
	chartPadT   = 16.0
	chartPadB   = 32.0
)

type chartView struct {
	Title  string
	Unit   string
	Empty  string
	Wide   bool
	SVG    template.HTML
	Legend []legendItem
}

type legendItem struct {
	Class string
	Label string
}

func presentCharts(charts []chart) []chartView {
	out := make([]chartView, len(charts))
	for i, item := range charts {
		view := chartView{Title: item.title, Unit: item.unit, Empty: item.empty, Wide: item.wide}
		if item.empty == "" {
			if _, _, ok := logSpan(item); ok {
				view.Unit = item.unit + ", log scale"
			}
			view.SVG = template.HTML(drawChart(item)) // #nosec G203 -- the graph escapes snapshot text; coordinates and class names are produced by the page
			view.Legend = legendOf(item.series)
		}
		out[i] = view
	}
	return out
}

func legendOf(series []chartSeries) []legendItem {
	out := make([]legendItem, len(series))
	for i, item := range series {
		out[i] = legendItem{Class: seriesClass(i), Label: item.name}
	}
	return out
}

func seriesClass(i int) string {
	return "series-" + strconv.Itoa(i%8)
}

func drawChart(item chart) string {
	var b strings.Builder
	_, _ = b.WriteString(`<svg class="plot" viewBox="0 0 640 220" role="img" aria-label="`)
	_, _ = b.WriteString(html.EscapeString(item.title))
	_, _ = b.WriteString(`"><title>`)
	_, _ = b.WriteString(html.EscapeString(item.title))
	_, _ = b.WriteString(`</title>`)
	maxV := maxValue(item.series) * 1.15
	if maxV <= 0 {
		maxV = 1
	}
	logLo, logHi, logarithmic := logSpan(item)
	start, end := axisRange(item)
	plotW := chartWidth - chartPadL - chartPadR
	plotH := chartHeight - chartPadT - chartPadB
	yPos := func(value float64) float64 {
		if !logarithmic {
			return yOf(value, maxV, plotH)
		}
		if value <= 0 {
			return chartPadT + plotH
		}
		frac := (math.Log10(value) - logLo) / (logHi - logLo)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		return chartPadT + (1-frac)*plotH
	}
	if logarithmic {
		for exp := int(math.Ceil(logLo)); exp <= int(math.Floor(logHi)); exp++ {
			value := math.Pow(10, float64(exp))
			writeGrid(&b, yPos(value), formatAxis(value))
		}
	} else {
		for tick := 0; tick <= 4; tick++ {
			writeGrid(&b, chartPadT+(1-float64(tick)/4)*plotH, formatAxis(maxV*float64(tick)/4))
		}
	}
	writeAxisTime(&b, start, chartPadL, "start")
	writeAxisTime(&b, start.Add(end.Sub(start)/2), chartPadL+plotW/2, "middle")
	writeAxisTime(&b, end, chartPadL+plotW, "end")
	for i, series := range item.series {
		class := seriesClass(i)
		for _, span := range series.spans {
			if len(span) >= 2 {
				_, _ = b.WriteString(`<polyline class="`)
				_, _ = b.WriteString(class)
				_, _ = b.WriteString(`" points="`)
				for n, point := range span {
					if n > 0 {
						_ = b.WriteByte(' ')
					}
					writeCoord(&b, xAt(start, end, point.at))
					_ = b.WriteByte(',')
					writeCoord(&b, yPos(point.value))
				}
				_, _ = b.WriteString(`"/>`)
			}
			for _, point := range span {
				_, _ = b.WriteString(`<circle class="`)
				_, _ = b.WriteString(class)
				_, _ = b.WriteString(`" r="3" cx="`)
				writeCoord(&b, xAt(start, end, point.at))
				_, _ = b.WriteString(`" cy="`)
				writeCoord(&b, yPos(point.value))
				_, _ = b.WriteString(`"><title>`)
				_, _ = b.WriteString(html.EscapeString(point.tip))
				_, _ = b.WriteString(`</title></circle>`)
			}
		}
	}
	_, _ = b.WriteString(`</svg>`)
	return b.String()
}

func writeGrid(b *strings.Builder, y float64, label string) {
	_, _ = b.WriteString(`<line class="grid" x1="`)
	writeCoord(b, chartPadL)
	_, _ = b.WriteString(`" y1="`)
	writeCoord(b, y)
	_, _ = b.WriteString(`" x2="`)
	writeCoord(b, chartWidth-chartPadR)
	_, _ = b.WriteString(`" y2="`)
	writeCoord(b, y)
	_, _ = b.WriteString(`"/>`)
	_, _ = b.WriteString(`<text class="axis" text-anchor="end" x="`)
	writeCoord(b, chartPadL-8)
	_, _ = b.WriteString(`" y="`)
	writeCoord(b, y+4)
	_, _ = b.WriteString(`">`)
	_, _ = b.WriteString(html.EscapeString(label))
	_, _ = b.WriteString(`</text>`)
}

func logSpan(item chart) (float64, float64, bool) {
	if !strings.Contains(item.title, "latency") {
		return 0, 0, false
	}
	minV := math.MaxFloat64
	var maxV float64
	for _, series := range item.series {
		for _, span := range series.spans {
			for _, point := range span {
				if point.value <= 0 || math.IsNaN(point.value) || math.IsInf(point.value, 0) {
					continue
				}
				if point.value < minV {
					minV = point.value
				}
				if point.value > maxV {
					maxV = point.value
				}
			}
		}
	}
	if minV == math.MaxFloat64 || maxV/minV < 10 {
		return 0, 0, false
	}
	lo := math.Floor(math.Log10(minV))
	hi := math.Ceil(math.Log10(maxV))
	if hi == lo {
		hi++
	}
	return lo, hi, true
}

func writeAxisTime(b *strings.Builder, at time.Time, x float64, anchor string) {
	_, _ = b.WriteString(`<text class="axis" text-anchor="`)
	_, _ = b.WriteString(anchor)
	_, _ = b.WriteString(`" x="`)
	writeCoord(b, x)
	_, _ = b.WriteString(`" y="`)
	writeCoord(b, chartHeight-8)
	_, _ = b.WriteString(`">`)
	_, _ = b.WriteString(at.UTC().Format("15:04:05"))
	_, _ = b.WriteString(`</text>`)
}

func axisRange(item chart) (time.Time, time.Time) {
	var start, end time.Time
	seen := false
	for _, series := range item.series {
		for _, span := range series.spans {
			for _, point := range span {
				if !seen || point.at.Before(start) {
					start = point.at
				}
				if !seen || point.at.After(end) {
					end = point.at
				}
				seen = true
			}
		}
	}
	if !seen {
		return item.start, item.end
	}
	if !start.Before(end) {
		return start.Add(-time.Second), end
	}
	return start, end
}

func xAt(start, end, at time.Time) float64 {
	plotW := chartWidth - chartPadL - chartPadR
	span := end.Sub(start)
	if span <= 0 {
		return chartPadL + plotW/2
	}
	frac := float64(at.Sub(start)) / float64(span)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return chartPadL + frac*plotW
}

func yOf(value, maxV, plotH float64) float64 {
	if maxV <= 0 {
		return chartPadT + plotH
	}
	frac := value / maxV
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return chartPadT + (1-frac)*plotH
}

func maxValue(series []chartSeries) float64 {
	var maxV float64
	for _, item := range series {
		for _, span := range item.spans {
			for _, point := range span {
				if math.IsNaN(point.value) || math.IsInf(point.value, 0) {
					continue
				}
				if point.value > maxV {
					maxV = point.value
				}
			}
		}
	}
	return maxV
}

func formatAxis(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	abs := math.Abs(v)
	switch {
	case abs == 0:
		return "0"
	case abs >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case abs >= 1:
		return strconv.FormatFloat(v, 'f', 2, 64)
	default:
		return strconv.FormatFloat(v, 'f', 3, 64)
	}
}

func writeCoord(b *strings.Builder, v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		_, _ = b.WriteString("0")
		return
	}
	_, _ = b.WriteString(strconv.FormatFloat(v, 'f', 2, 64))
}
