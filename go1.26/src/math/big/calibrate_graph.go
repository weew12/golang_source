// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// 本程序将以下命令打印的 CSV 校准数据
//
//	go test -run=Calibrate/Name -calibrate >file.csv
//
// 转换为 SVG 文件。调用方式：
//
//	go run calibrate_graph.go file.csv >file.svg
//
// 详见 calibrate.md。

package main

import (
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: go run calibrate_graph.go file.csv >file.svg\n")
	os.Exit(2)
}

// A Point 是所绘制数据中的一个 X、Y 坐标。
type Point struct {
	X, Y float64
}

// A Graph 是要绘制为 SVG 的图。
type Graph struct {
	Title   string    // 图上方标题
	Geomean []Point   // 几何平均线
	Lines   [][]Point // 归一化数据线
	XAxis   string    // x 轴标签
	YAxis   string    // y 轴标签
	Min     Point     // 数据显示的最小点
	Max     Point     // 数据显示的最大点
}

var yMax = flag.Float64("ymax", 1.2, "maximum y axis value")
var alphaNorm = flag.Float64("alphanorm", 0.1, "alpha for a single norm line")

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 {
		usage()
	}

	// 读取 CSV。它可能被包裹在
	//	-- name.csv --
	//	...
	//	-- eof --
	// 框架中，此时移除该框架。
	fdata, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	if _, after, ok := bytes.Cut(fdata, []byte(".csv --\n")); ok {
		fdata = after
	}
	if before, _, ok := bytes.Cut(fdata, []byte("-- eof --\n")); ok {
		fdata = before
	}
	rd := csv.NewReader(bytes.NewReader(fdata))
	rd.FieldsPerRecord = -1
	records, err := rd.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	// 从加载的 CSV 构造图。
	// CSV 以元数据行开头，例如
	//	goos,darwin
	// 然后包含两张计时表。
	// 每张表看起来像
	//	size \ threshold,10,20,30,40
	//	100,1,2,3,4
	//	200,2,3,4,5
	//	300,3,4,5,6
	//	400,4,5,6,7
	//	500,5,6,7,8
	// 表头行给出阈值，随后每一行
	// 给出一个输入大小以及每个阈值的计时。
	// 省略的计时是空字符串，解析时会变成无穷大。
	// 第一张表给出原始纳秒计时。
	// 第二张表给出相对于给定输入大小下最快
	// 可能阈值的归一化计时。
	// 我们只需要第二张表。
	// 两张表之后是所有归一化计时针对每个阈值的
	// 几何平均列表：
	//	geomean,1.2,1.1,1.0,1.4
	// 我们将每一行归一化计时变成图中的一条线，
	// 并将几何平均变成一条叠加的粗线。
	// 元数据用于准备标题。
	g := &Graph{
		YAxis: "Relative Slowdown",
		Min:   Point{0, 1},
		Max:   Point{1, 1.2},
	}
	meta := make(map[string]string)
	table := 0 // number of table headers seen
	var thresholds []float64
	maxNorm := 0.0
	for _, rec := range records {
		if len(rec) == 0 {
			continue
		}
		if len(rec) == 2 {
			meta[rec[0]] = rec[1]
			continue
		}
		if rec[0] == `size \ threshold` {
			table++
			if table == 2 {
				thresholds = parseFloats(rec)
				g.Min.X = thresholds[0]
				g.Max.X = thresholds[len(thresholds)-1]
			}
			continue
		}
		if rec[0] == "geomean" {
			table = 3 // end of norms table
			geomeans := parseFloats(rec)
			g.Geomean = floatsToLine(thresholds, geomeans)
			continue
		}
		if table == 2 {
			if _, err := strconv.Atoi(rec[0]); err != nil { // size
				log.Fatalf("invalid table line: %q", rec)
			}
			norms := parseFloats(rec)
			if len(norms) > len(thresholds) {
				log.Fatalf("too many timings (%d > %d): %q", len(norms), len(thresholds), rec)
			}
			g.Lines = append(g.Lines, floatsToLine(thresholds, norms))
			for _, y := range norms {
				maxNorm = max(maxNorm, y)
			}
			continue
		}
	}

	g.Max.Y = min(*yMax, math.Ceil(maxNorm*100)/100)
	g.XAxis = meta["calibrate"] + "Threshold"
	g.Title = meta["goos"] + "/" + meta["goarch"] + " " + meta["cpu"]

	os.Stdout.Write(g.SVG())
}

// parseFloats 将 rec[1:] 解析为浮点值。
// 如果某个字段是空字符串，则将其表示为 +Inf。
func parseFloats(rec []string) []float64 {
	floats := make([]float64, 0, len(rec)-1)
	for _, v := range rec[1:] {
		if v == "" {
			floats = append(floats, math.Inf(+1))
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Fatalf("invalid record: %q (%v)", rec, err)
		}
		floats = append(floats, f)
	}
	return floats
}

// floatsToLine 将一串浮点数转换为一条线，忽略缺失（无穷）的值。
func floatsToLine(x, y []float64) []Point {
	var line []Point
	for i, yi := range y {
		if !math.IsInf(yi, 0) {
			line = append(line, Point{x[i], yi})
		}
	}
	return line
}

const svgHeader = `<svg width="%d" height="%d" version="1.1" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <style type="text/css"><![CDATA[
      text { stroke-width: 0; white-space: pre; }
      text.hjc { text-anchor: middle; }
      text.hjl { text-anchor: start; }
      text.hjr { text-anchor: end; }
      .def { stroke-linecap: round; stroke-linejoin: round; fill: none; stroke: #000000; stroke-width: 1px; }
      .tick { stroke: #000000; fill: #000000; font: %dpx Times; }
      .title { stroke: #000000; fill: #000000; font: %dpx Times; font-weight: bold; }
      .axis { stroke-width: 2px; }
      .norm { stroke: rgba(0,0,0,%f); }
      .geomean { stroke: #6666ff; stroke-width: 2px; }
    ]]></style>
  </defs>
  <g class="def">
`

// 绘制图的布局常量
const (
	DX   = 600          // 所绘数据的宽度
	DY   = 150          // 所绘数据的高度
	ML   = 80           // 左边距
	MT   = 30           // 上边距
	MR   = 10           // 右边距
	MB   = 50           // 下边距
	PS   = 14           // 文本点大小
	W    = ML + DX + MR // 整个图的宽度
	H    = MT + DY + MB // 整个图的高度
	Tick = 5            // 坐标轴刻度长度
)

// An SVGPoint 是 SVG 图像中的一个点，以像素为单位，
// Y 向下递增。
type SVGPoint struct {
	X, Y int
}

func (p SVGPoint) String() string {
	return fmt.Sprintf("%d,%d", p.X, p.Y)
}

// pt 将 x、y 数据值（例如来自一个 Point）转换为 SVGPoint。
func (g *Graph) pt(x, y float64) SVGPoint {
	return SVGPoint{
		X: ML + int((x-g.Min.X)/(g.Max.X-g.Min.X)*DX),
		Y: H - MB - int((y-g.Min.Y)/(g.Max.Y-g.Min.Y)*DY),
	}
}

// SVG 返回该图的 SVG 文本。
func (g *Graph) SVG() []byte {

	var svg bytes.Buffer
	fmt.Fprintf(&svg, svgHeader, W, H, PS, PS, *alphaNorm)

	// 绘制数据，已裁剪。
	fmt.Fprintf(&svg, "<clipPath id=\"cp\"><path d=\"M %v L %v L %v L %v Z\" /></clipPath>\n",
		g.pt(g.Min.X, g.Min.Y), g.pt(g.Max.X, g.Min.Y), g.pt(g.Max.X, g.Max.Y), g.pt(g.Min.X, g.Max.Y))
	fmt.Fprintf(&svg, "<g clip-path=\"url(#cp)\">\n")
	for _, line := range g.Lines {
		if len(line) == 0 {
			continue
		}
		fmt.Fprintf(&svg, "<path class=\"norm\" d=\"M %v", g.pt(line[0].X, line[0].Y))
		for _, v := range line[1:] {
			fmt.Fprintf(&svg, " L %v", g.pt(v.X, v.Y))
		}
		fmt.Fprintf(&svg, "\"/>\n")
	}
	// 绘制几何平均。
	if len(g.Geomean) > 0 {
		line := g.Geomean
		fmt.Fprintf(&svg, "<path class=\"geomean\" d=\"M %v", g.pt(line[0].X, line[0].Y))
		for _, v := range line[1:] {
			fmt.Fprintf(&svg, " L %v", g.pt(v.X, v.Y))
		}
		fmt.Fprintf(&svg, "\"/>\n")
	}
	fmt.Fprintf(&svg, "</g>\n")

	// 绘制坐标轴以及主、次刻度线。
	fmt.Fprintf(&svg, "<path class=\"axis\" d=\"")
	fmt.Fprintf(&svg, " M %v L %v", g.pt(g.Min.X, g.Min.Y), g.pt(g.Max.X, g.Min.Y)) // x 轴
	fmt.Fprintf(&svg, " M %v L %v", g.pt(g.Min.X, g.Min.Y), g.pt(g.Min.X, g.Max.Y)) // y 轴
	xscale := 10.0
	if g.Max.X-g.Min.X < 100 {
		xscale = 1.0
	}
	for x := int(math.Ceil(g.Min.X / xscale)); float64(x)*xscale <= g.Max.X; x++ {
		if x%5 != 0 {
			fmt.Fprintf(&svg, " M %v l 0,%d", g.pt(float64(x)*xscale, g.Min.Y), Tick)
		} else {
			fmt.Fprintf(&svg, " M %v l 0,%d", g.pt(float64(x)*xscale, g.Min.Y), 2*Tick)
		}
	}
	yscale := 100.0
	if g.Max.Y-g.Min.Y > 0.5 {
		yscale = 10
	}
	for y := int(math.Ceil(g.Min.Y * yscale)); float64(y) <= g.Max.Y*yscale; y++ {
		if y%5 != 0 {
			fmt.Fprintf(&svg, " M %v l -%d,0", g.pt(g.Min.X, float64(y)/yscale), Tick)
		} else {
			fmt.Fprintf(&svg, " M %v l -%d,0", g.pt(g.Min.X, float64(y)/yscale), 2*Tick)
		}
	}
	fmt.Fprintf(&svg, "\"/>\n")

	// 在主刻度上绘制刻度标签。
	for x := int(math.Ceil(g.Min.X / xscale)); float64(x)*xscale <= g.Max.X; x++ {
		if x%5 == 0 {
			p := g.pt(float64(x)*xscale, g.Min.Y)
			fmt.Fprintf(&svg, "<text x=\"%d\" y=\"%d\" class=\"tick hjc\">%d</text>\n", p.X, p.Y+2*Tick+PS, x*int(xscale))
		}
	}
	for y := int(math.Ceil(g.Min.Y * yscale)); float64(y) <= g.Max.Y*yscale; y++ {
		if y%5 == 0 {
			p := g.pt(g.Min.X, float64(y)/yscale)
			fmt.Fprintf(&svg, "<text x=\"%d\" y=\"%d\" class=\"tick hjr\">%.2f</text>\n", p.X-2*Tick-Tick, p.Y+PS/3, float64(y)/yscale)
		}
	}

	// 绘制图标题和坐标轴标题。
	fmt.Fprintf(&svg, "<text x=\"%d\" y=\"%d\" class=\"title hjc\">%s</text>\n", ML+DX/2, MT-PS/3, g.Title)
	fmt.Fprintf(&svg, "<text x=\"%d\" y=\"%d\" class=\"title hjc\">%s</text>\n", ML+DX/2, MT+DY+2*Tick+2*PS+PS/2, g.XAxis)
	fmt.Fprintf(&svg, "<g transform=\"translate(%d,%d) rotate(-90)\"><text x=\"0\" y=\"0\" class=\"title hjc\">%s</text></g>\n", ML-Tick-Tick-3*PS, MT+DY/2, g.YAxis)

	fmt.Fprintf(&svg, "</g></svg>\n")
	return svg.Bytes()
}
