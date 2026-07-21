package server

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/brogergvhs/kaodoku/internal/service"
)

// donutColors cycles theme tokens for segment fills so charts stay on-theme.
var donutColors = []string{
	"var(--color-primary)", "var(--color-secondary)", "var(--color-info)",
	"var(--color-success)", "var(--color-warning)", "var(--color-error)",
}

// lineChart renders a day series as a themed area+line SVG that scales to its
// container width.
func lineChart(points []service.DayCount) template.HTML {
	if len(points) < 2 {
		return `<div class="text-sm text-base-content/50 py-6 text-center">Not enough data yet.</div>`
	}
	const w, h, pad = 720.0, 140.0, 6.0
	var max int64 = 1
	for _, p := range points {
		if p.Count > max {
			max = p.Count
		}
	}
	n := len(points)
	x := func(i int) float64 { return pad + float64(i)/float64(n-1)*(w-2*pad) }
	y := func(v int64) float64 { return h - pad - float64(v)/float64(max)*(h-2*pad) }
	var line, area strings.Builder
	for i, p := range points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.1f %.1f ", cmd, x(i), y(p.Count))
	}
	fmt.Fprintf(&area, "M%.1f %.1f ", x(0), h-pad)
	for i, p := range points {
		fmt.Fprintf(&area, "L%.1f %.1f ", x(i), y(p.Count))
	}
	fmt.Fprintf(&area, "L%.1f %.1f Z", x(n-1), h-pad)
	svg := fmt.Sprintf(`<svg viewBox="0 0 %g %g" preserveAspectRatio="none" class="w-full" style="height:140px" role="img">`+
		`<path d="%s" fill="var(--color-primary)" opacity="0.14"/>`+
		`<path d="%s" fill="none" stroke="var(--color-primary)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`+
		`</svg>`, w, h, area.String(), strings.TrimSpace(line.String()))
	return template.HTML(svg) //nolint:gosec // values are numeric/constant
}

// heatmap renders a GitHub-style calendar of daily activity.
func heatmap(days []service.DayCount) template.HTML {
	if len(days) == 0 {
		return ""
	}
	var max int64 = 1
	for _, d := range days {
		if d.Count > max {
			max = d.Count
		}
	}
	const cell, gap = 12.0, 3.0
	weeks := (len(days) + 6) / 7
	w := float64(weeks)*(cell+gap) + gap
	hgt := 7*(cell+gap) + gap
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" class="w-full" style="max-height:132px" role="img">`, w, hgt)
	for i, d := range days {
		col := i / 7
		row := i % 7
		x := gap + float64(col)*(cell+gap)
		y := gap + float64(row)*(cell+gap)
		op := 0.0
		if d.Count > 0 {
			op = 0.25 + 0.75*float64(d.Count)/float64(max)
		}
		fill := "var(--color-neutral)"
		if op > 0 {
			fill = "var(--color-primary)"
		}
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%g" height="%g" rx="2" fill="%s" fill-opacity="%.2f"><title>%s: %d</title></rect>`,
			x, y, cell, cell, fill, op, d.Day, d.Count)
	}
	b.WriteString("</svg>")
	return template.HTML(b.String()) //nolint:gosec
}

// donut renders a proportional ring for a small set of named counts.
func donut(items []service.NamedCount) template.HTML {
	var total int64
	for _, it := range items {
		total += it.Count
	}
	if total == 0 {
		return `<div class="text-sm text-base-content/50 py-6 text-center">No data yet.</div>`
	}
	const r, cx, cy, sw = 42.0, 60.0, 60.0, 16.0
	circ := 2 * 3.14159265 * r
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 120 120" class="mx-auto" style="width:120px;height:120px" role="img">`)
	fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="%g" fill="none" stroke="var(--color-neutral)" stroke-width="%g"/>`, cx, cy, r, sw)
	var offset float64
	for i, it := range items {
		frac := float64(it.Count) / float64(total)
		dash := frac * circ
		color := donutColors[i%len(donutColors)]
		fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="%g" fill="none" stroke="%s" stroke-width="%g" `+
			`stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f" transform="rotate(-90 %g %g)"><title>%s: %.0f%%</title></circle>`,
			cx, cy, r, color, sw, dash, circ-dash, -offset, cx, cy, template.HTMLEscapeString(it.Name), frac*100)
		offset += dash
	}
	b.WriteString("</svg>")
	return template.HTML(b.String()) //nolint:gosec
}

// donutColor returns the legend swatch color for the i-th segment.
func donutColor(i int) template.CSS { return template.CSS(donutColors[i%len(donutColors)]) }
