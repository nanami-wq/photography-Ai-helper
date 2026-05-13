package photoscorer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const resizeLongEdge = 512

// Metrics 从像素可重复计算的客观量（均已压到 0–100，数值越大通常表示该维度「越理想」）。
type Metrics struct {
	Sharpness0_100            float64 `json:"sharpness_0_100"`
	Exposure0_100             float64 `json:"exposure_0_100"`
	ColorRichness0_100        float64 `json:"color_richness_0_100"`         // 像素 RGB 色度均值
	ColorColorfulness0_100    float64 `json:"color_colorfulness_0_100"`     // Hasler–Süsstrunk 风格 colorfulness
	ColorBalance0_100         float64 `json:"color_balance_0_100"`          // 中灰区 RGB 一致性，偏高表示偏色/通道失衡较轻
	ColorSaturationIdeal0_100 float64 `json:"color_saturation_ideal_0_100"` // HSV 饱和度相对「舒适区」的贴合度
	ColorCV0_100              float64 `json:"color_cv_0_100"`               // 色彩客观综合分，用于与 LLM 融合
	TechniqueCV0_100          float64 `json:"technique_cv_0_100"`
	CompositionCV0_100        float64 `json:"composition_cv_0_100"`
	CompositionFocusRatio     float64 `json:"composition_focus_ratio"` // Sobel 能量 max/mean，越大表示主体/边缘更集中
	CompositionThirdsDist     float64 `json:"composition_thirds_dist"` // 归一化重心到最近三分/黄金线的最小距离，便于排错
}

// FusionWeights 与 LLM 子分的融合方式（算法侧可版本化，无需改 prompt）。
const FusionWeightsNote = "color=0.34~0.40*LLM+(1-w)*color_cv; color_cv=0.26*rich+0.28*colorful+0.26*balance+0.20*sat_ideal; technique=0.28~0.42*LLM+(1-w)*tech_cv; composition=0.36~0.52*LLM+(1-w)*comp_cv; tech_cv=0.65*sharp+0.35*exp; comp_cv=0.72*geom+0.28*lr_balance"

// Result 融合后的分数与明细。
type Result struct {
	Enabled              bool    `json:"enabled"`
	FinalColor           int     `json:"final_color"`
	FinalComposition     int     `json:"final_composition"`
	FinalTechnique       int     `json:"final_technique"`
	LLMColor             int     `json:"llm_color"`
	LLMComposition       int     `json:"llm_composition"`
	LLMTechnique         int     `json:"llm_technique"`
	Metrics              Metrics `json:"metrics"`
	TechniqueLLMWeight   float64 `json:"technique_llm_weight"`
	ColorLLMWeight       float64 `json:"color_llm_weight"`
	CompositionLLMWeight float64 `json:"composition_llm_weight"`
}

func clamp100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func clampInt(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// MetricsFromImage 在固定长边缩放后计算 Laplacian 方差、直方图曝光、平均色度。
func MetricsFromImage(im image.Image) (Metrics, error) {
	if im == nil {
		return Metrics{}, fmt.Errorf("nil image")
	}
	rgba := resizeToRGBA(im, resizeLongEdge)
	gray := rgbaToGray(rgba)
	sharp := sharpnessFromLaplacian(gray)
	exp := exposureScoreFromLuma(rgba)
	chroma := colorRichnessFromRGBA(rgba)
	cf := colorfulnessScoreFromRGBA(rgba)
	bal := colorBalanceMidtoneScore(rgba)
	satI := saturationIdealnessFromRGBA(rgba)
	colorCV := 0.26*chroma + 0.28*cf + 0.26*bal + 0.20*satI
	colorCV = clamp100(colorCV)
	techCV := 0.65*sharp + 0.35*exp
	compCV, focusR, thirdsDist := compositionCVFromGray(gray)
	return Metrics{
		Sharpness0_100:            sharp,
		Exposure0_100:             exp,
		ColorRichness0_100:        chroma,
		ColorColorfulness0_100:    cf,
		ColorBalance0_100:         bal,
		ColorSaturationIdeal0_100: satI,
		ColorCV0_100:              colorCV,
		TechniqueCV0_100:          techCV,
		CompositionCV0_100:        compCV,
		CompositionFocusRatio:     focusR,
		CompositionThirdsDist:     thirdsDist,
	}, nil
}

// MetricsFromBytes 解码常见格式（含 webp）；失败则返回 error，由调用方回退为纯 LLM。
func MetricsFromBytes(b []byte) (Metrics, error) {
	im, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return Metrics{}, err
	}
	return MetricsFromImage(im)
}

// FuseLLMWithMetrics 将 LLM 三项子分与客观量融合（构图：Sobel 能量重心 + 三分/黄金线 + 左右平衡）。
func FuseLLMWithMetrics(llmC, llmCo, llmT int, m Metrics) Result {
	llmC, llmCo, llmT = clampInt(llmC), clampInt(llmCo), clampInt(llmT)
	wColorLLM := 0.38
	wTechLLM := 0.42
	wCoLLM := 0.42
	if m.ColorBalance0_100 < 38 {
		wColorLLM = 0.34
	}
	if m.Sharpness0_100 < 22 {
		wTechLLM = 0.28
	} else if m.Sharpness0_100 < 35 {
		wTechLLM = 0.35
	}
	fr := m.CompositionFocusRatio
	if fr < 1.55 {
		wCoLLM = 0.52
	} else if fr > 3.8 {
		wCoLLM = 0.36
	}
	cF := wColorLLM*float64(llmC) + (1-wColorLLM)*m.ColorCV0_100
	tF := wTechLLM*float64(llmT) + (1-wTechLLM)*m.TechniqueCV0_100
	coF := wCoLLM*float64(llmCo) + (1-wCoLLM)*m.CompositionCV0_100
	return Result{
		Enabled:              true,
		FinalColor:           clampInt(int(math.Round(cF))),
		FinalComposition:     clampInt(int(math.Round(coF))),
		FinalTechnique:       clampInt(int(math.Round(tF))),
		LLMColor:             llmC,
		LLMComposition:       llmCo,
		LLMTechnique:         llmT,
		Metrics:              m,
		TechniqueLLMWeight:   wTechLLM,
		ColorLLMWeight:       wColorLLM,
		CompositionLLMWeight: wCoLLM,
	}
}

func resizeToRGBA(src image.Image, maxEdge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	nw, nh := w, h
	if w >= h && w > maxEdge {
		nw = maxEdge
		nh = max(1, h*maxEdge/w)
	} else if h > w && h > maxEdge {
		nh = maxEdge
		nw = max(1, w*maxEdge/h)
	}
	if nw == w && nh == h {
		return copyToRGBA(src)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func copyToRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, src, b.Min, draw.Src)
	return out
}

func rgbaToGray(im *image.RGBA) *image.Gray {
	b := im.Bounds()
	g := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			y8 := (19595*r16 + 38470*g16 + 7471*b16 + 1<<15) >> 24
			g.SetGray(x, y, color.Gray{Y: uint8(y8)})
		}
	}
	return g
}

func laplacianVariance(g *image.Gray) float64 {
	b := g.Bounds()
	if b.Dx() < 3 || b.Dy() < 3 {
		return 0
	}
	var sum, sumsq float64
	var n float64
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		for x := b.Min.X + 1; x < b.Max.X-1; x++ {
			c := float64(g.GrayAt(x, y).Y)
			t := float64(g.GrayAt(x, y-1).Y)
			bt := float64(g.GrayAt(x, y+1).Y)
			l := float64(g.GrayAt(x-1, y).Y)
			r := float64(g.GrayAt(x+1, y).Y)
			v := t + bt + l + r - 4*c
			sum += v
			sumsq += v * v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	mean := sum / n
	return sumsq/n - mean*mean
}

func sharpnessFromLaplacian(g *image.Gray) float64 {
	v := laplacianVariance(g)
	// 方差跨图像尺度大，用对数映射到 0–100（经验区间，可按数据集再标定）
	s := math.Log1p(v)
	lo, hi := 3.0, 9.0 // 约对应极糊 ~ 较锐
	if s <= lo {
		return 0
	}
	if s >= hi {
		return 100
	}
	return clamp100((s - lo) / (hi - lo) * 100)
}

func exposureScoreFromLuma(im *image.RGBA) float64 {
	b := im.Bounds()
	var hist [256]int
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			y8 := int((19595*r16 + 38470*g16 + 7471*b16 + 1<<15) >> 24)
			hist[y8]++
			n++
		}
	}
	if n == 0 {
		return 50
	}
	clip := 0
	for i := 0; i < 8; i++ {
		clip += hist[i]
	}
	for i := 248; i < 256; i++ {
		clip += hist[i]
	}
	frac := float64(clip) / float64(n)
	// 死黑/死白比例越高，曝光分越低
	penalty := math.Min(1, frac*6)
	return clamp100(100 * (1 - penalty))
}

func colorRichnessFromRGBA(im *image.RGBA) float64 {
	b := im.Bounds()
	var sum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			r := float64(r16 >> 8)
			g := float64(g16 >> 8)
			bb := float64(b16 >> 8)
			mx := r
			if g > mx {
				mx = g
			}
			if bb > mx {
				mx = bb
			}
			mn := r
			if g < mn {
				mn = g
			}
			if bb < mn {
				mn = bb
			}
			sum += (mx - mn) / 255.0
			n++
		}
	}
	if n == 0 {
		return 50
	}
	ch := sum / float64(n)
	// 灰度图 chroma 接近 0；鲜艳图可到 0.2+
	lo, hi := 0.03, 0.20
	if ch <= lo {
		return clamp100(15 + ch/lo*25)
	}
	if ch >= hi {
		return 100
	}
	return clamp100(40 + (ch-lo)/(hi-lo)*55)
}

// colorfulnessScoreFromRGBA 基于 Hasler & Süsstrunk 的 colorfulness 思路（rg/yb 统计），映射为 0–100。
func colorfulnessScoreFromRGBA(im *image.RGBA) float64 {
	b := im.Bounds()
	n := 0
	var sumRg, sumYb float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			R := float64(r16 >> 8)
			G := float64(g16 >> 8)
			B := float64(b16 >> 8)
			sumRg += R - G
			sumYb += 0.5*(R+G) - B
			n++
		}
	}
	if n == 0 {
		return 40
	}
	mRg := sumRg / float64(n)
	mYb := sumYb / float64(n)
	var ssrg, ssyb float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			R := float64(r16 >> 8)
			G := float64(g16 >> 8)
			B := float64(b16 >> 8)
			drg := (R - G) - mRg
			dyb := (0.5*(R+G) - B) - mYb
			ssrg += drg * drg
			ssyb += dyb * dyb
		}
	}
	stdRg := math.Sqrt(ssrg / float64(n))
	stdYb := math.Sqrt(ssyb / float64(n))
	rootMean := math.Hypot(mRg, mYb)
	rootStd := math.Hypot(stdRg, stdYb)
	F := rootStd + 0.3*rootMean
	if F <= 4 {
		return clamp100(8 + F*4)
	}
	if F >= 55 {
		return 100
	}
	return clamp100(12 + (F-4)/51*88)
}

// colorBalanceMidtoneScore 在中灰亮度像素上看 RGB 通道是否「像灰」；差越大视为偏色/通道失衡越重（启发式，肤色片会略吃亏）。
func colorBalanceMidtoneScore(im *image.RGBA) float64 {
	b := im.Bounds()
	var sumR, sumG, sumB float64
	cnt := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			R := float64(r16 >> 8)
			G := float64(g16 >> 8)
			B := float64(b16 >> 8)
			Y := 0.299*R + 0.587*G + 0.114*B
			if Y < 42 || Y > 218 {
				continue
			}
			sumR += R
			sumG += G
			sumB += B
			cnt++
		}
	}
	if cnt < 48 {
		return 52
	}
	mr := sumR / float64(cnt)
	mg := sumG / float64(cnt)
	mb := sumB / float64(cnt)
	cast := (math.Abs(mr-mg) + math.Abs(mg-mb) + math.Abs(mr-mb)) / 3.0
	return clamp100(100 - math.Min(95, cast*1.32))
}

func rgbToHSV01(r, g, b float64) (h, s, v float64) {
	r /= 255
	g /= 255
	b /= 255
	maxc := math.Max(r, math.Max(g, b))
	minc := math.Min(r, math.Min(g, b))
	v = maxc
	d := maxc - minc
	if maxc < 1e-9 {
		return 0, 0, v
	}
	s = d / maxc
	if d < 1e-9 {
		return 0, s, v
	}
	var hh float64
	switch {
	case math.Abs(maxc-r) < 1e-6:
		hh = math.Mod((g-b)/d+6, 6)
	case math.Abs(maxc-g) < 1e-6:
		hh = (b-r)/d + 2
	default:
		hh = (r-g)/d + 4
	}
	h = hh / 6
	return h, s, v
}

// saturationIdealnessFromRGBA 对足够亮像素的平均饱和度，相对「常见舒适区」给分；过灰与过饱都压低。
func saturationIdealnessFromRGBA(im *image.RGBA) float64 {
	b := im.Bounds()
	var sumS float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := im.At(x, y).RGBA()
			_, s, v := rgbToHSV01(float64(r16>>8), float64(g16>>8), float64(b16>>8))
			if v < 0.11 {
				continue
			}
			sumS += s
			n++
		}
	}
	if n == 0 {
		return 32
	}
	meanS := sumS / float64(n)
	return saturationIdealnessFromMeanS(meanS)
}

func saturationIdealnessFromMeanS(meanS float64) float64 {
	if meanS <= 0.035 {
		return clamp100(20 + meanS/0.035*28)
	}
	if meanS >= 0.86 {
		return clamp100(44)
	}
	center, sigma := 0.41, 0.21
	d := meanS - center
	return clamp100(100 * math.Exp(-(d*d)/(2*sigma*sigma)))
}

// compositionCVFromGray 用 Sobel 梯度幅度的加权重心近似「视觉重心」，再与三分/黄金分割线距离及左右能量对称处理结合。
func compositionCVFromGray(g *image.Gray) (cv100, focusRatio, thirdsDist float64) {
	b := g.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 5 || h < 5 {
		return 45, 1, 0.2
	}
	var sumMag, sumXF, sumYF float64
	var maxMag float64
	var leftSum, rightSum float64
	midLocal := float64(w) / 2
	n := 0
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		for x := b.Min.X + 1; x < b.Max.X-1; x++ {
			m := sobelGradientMag(g, x, y)
			sumMag += m
			xL := float64(x - b.Min.X)
			yL := float64(y - b.Min.Y)
			sumXF += xL * m
			sumYF += yL * m
			if m > maxMag {
				maxMag = m
			}
			if xL < midLocal {
				leftSum += m
			} else {
				rightSum += m
			}
			n++
		}
	}
	if sumMag < 1e-9 || n == 0 {
		return 45, 1, 0.25
	}
	meanMag := sumMag / float64(n)
	focusRatio = maxMag / (meanMag + 1e-9)

	cx := sumXF / sumMag
	cy := sumYF / sumMag
	fw := float64(w - 1)
	fh := float64(h - 1)
	if fw < 1 {
		fw = 1
	}
	if fh < 1 {
		fh = 1
	}
	nx := clamp01(cx / fw)
	ny := clamp01(cy / fh)
	dLine := minDistToCompositionLines(nx, ny)
	thirdsDist = dLine
	geom := alignmentScoreFromLineDist(dLine)

	sym := math.Min(leftSum, rightSum) / (math.Max(leftSum, rightSum) + 1e-9)
	balanceScore := clamp100(32 + 68*sym)

	cv100 = clamp100(0.72*geom + 0.28*balanceScore)
	return cv100, focusRatio, thirdsDist
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func sobelGradientMag(g *image.Gray, x, y int) float64 {
	tl := float64(g.GrayAt(x-1, y-1).Y)
	t := float64(g.GrayAt(x, y-1).Y)
	tr := float64(g.GrayAt(x+1, y-1).Y)
	l := float64(g.GrayAt(x-1, y).Y)
	r := float64(g.GrayAt(x+1, y).Y)
	bl := float64(g.GrayAt(x-1, y+1).Y)
	bt := float64(g.GrayAt(x, y+1).Y)
	br := float64(g.GrayAt(x+1, y+1).Y)
	gx := -tl + tr - 2*l + 2*r - bl + br
	gy := -tl - 2*t - tr + bl + 2*bt + br
	return math.Hypot(gx, gy)
}

func minDistToCompositionLines(nx, ny float64) float64 {
	thirds := []float64{1.0 / 3, 2.0 / 3}
	golden := []float64{0.3819660112501051, 0.6180339887498949}
	minD := 1.0
	for _, v := range thirds {
		d := math.Abs(nx - v)
		if d < minD {
			minD = d
		}
		d = math.Abs(ny - v)
		if d < minD {
			minD = d
		}
	}
	for _, v := range golden {
		d := math.Abs(nx - v)
		if d < minD {
			minD = d
		}
		d = math.Abs(ny - v)
		if d < minD {
			minD = d
		}
	}
	return minD
}

func alignmentScoreFromLineDist(d float64) float64 {
	if d <= 0.012 {
		return 100
	}
	if d >= 0.30 {
		return clamp100(38 - (d-0.30)*45)
	}
	return clamp100(100 - (d-0.012)/0.288*62)
}
