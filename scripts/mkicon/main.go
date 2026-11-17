// Command mkicon renders the devknife app icon (PNG master + macOS .iconset)
// with no external tools: `go run ./scripts/mkicon -out build/icon`.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

const k = 0.5522847498 // cubic Bézier circle constant

type canvas struct {
	r     *vector.Rasterizer
	scale float32
	off   float32
}

func (c *canvas) pt(x, y float32) (float32, float32) { return c.off + x*c.scale, c.off + y*c.scale }
func (c *canvas) move(x, y float32)                  { c.r.MoveTo(c.pt(x, y)) }
func (c *canvas) line(x, y float32)                  { c.r.LineTo(c.pt(x, y)) }
func (c *canvas) cube(x1, y1, x2, y2, x, y float32) {
	ax, ay := c.pt(x1, y1)
	bx, by := c.pt(x2, y2)
	px, py := c.pt(x, y)
	c.r.CubeTo(ax, ay, bx, by, px, py)
}

func (c *canvas) circle(cx, cy, r float32) {
	d := r * k
	c.move(cx+r, cy)
	c.cube(cx+r, cy+d, cx+d, cy+r, cx, cy+r)
	c.cube(cx-d, cy+r, cx-r, cy+d, cx-r, cy)
	c.cube(cx-r, cy-d, cx-d, cy-r, cx, cy-r)
	c.cube(cx+d, cy-r, cx+r, cy-d, cx+r, cy)
	c.r.ClosePath()
}

func (c *canvas) roundRect(x, y, w, h, r float32) {
	d := r * k
	c.move(x+r, y)
	c.line(x+w-r, y)
	c.cube(x+w-r+d, y, x+w, y+r-d, x+w, y+r)
	c.line(x+w, y+h-r)
	c.cube(x+w, y+h-r+d, x+w-r+d, y+h, x+w-r, y+h)
	c.line(x+r, y+h)
	c.cube(x+r-d, y+h, x, y+h-r+d, x, y+h-r)
	c.line(x, y+r)
	c.cube(x, y+r-d, x+r-d, y, x+r, y)
	c.r.ClosePath()
}

// thick segment (round caps are drawn separately)
func (c *canvas) stroke(w float32, pts ...[2]float32) {
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := float32(math.Hypot(float64(dx), float64(dy)))
		nx, ny := -dy/l*w/2, dx/l*w/2
		c.move(a[0]+nx, a[1]+ny)
		c.line(b[0]+nx, b[1]+ny)
		c.line(b[0]-nx, b[1]-ny)
		c.line(a[0]-nx, a[1]-ny)
		c.r.ClosePath()
	}
}

func fill(dst *image.RGBA, size int, src image.Image, paint func(c *canvas)) {
	r := vector.NewRasterizer(size, size)
	r.DrawOp = draw.Over
	m := float32(size) * 100 / 1024 // Big Sur style margin
	paint(&canvas{r: r, scale: (float32(size) - 2*m) / 32, off: m})
	r.Draw(dst, dst.Bounds(), src, image.Point{})
}

type gradient struct {
	top, bottom color.RGBA
	h           int
}

func (g gradient) ColorModel() color.Model { return color.RGBAModel }
func (g gradient) Bounds() image.Rectangle { return image.Rect(-1e9, -1e9, 1e9, 1e9) }
func (g gradient) At(x, y int) color.Color {
	t := math.Max(0, math.Min(1, float64(y)/float64(g.h)))
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return color.RGBA{mix(g.top.R, g.bottom.R), mix(g.top.G, g.bottom.G), mix(g.top.B, g.bottom.B), 255}
}

func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// subtle shadow
	fill(img, size, image.NewUniform(color.RGBA{0, 0, 0, 40}), func(c *canvas) { c.roundRect(0, 0.45, 32, 32, 7.2) })
	fill(img, size, gradient{color.RGBA{0x6d, 0x63, 0xf6, 255}, color.RGBA{0x3f, 0x37, 0xc9, 255}, size}, func(c *canvas) { c.roundRect(0, 0, 32, 32, 7.2) })
	// blade (each primitive in its own pass: overlapping sub-paths with
	// opposite winding would otherwise cancel out)
	white := image.NewUniform(color.RGBA{255, 255, 255, 255})
	fill(img, size, white, func(c *canvas) {
		c.move(9, 21.5)
		c.line(19.5, 11)
		c.line(23, 14.5)
		c.line(12.5, 25)
		c.line(9, 25)
		c.r.ClosePath()
	})
	fill(img, size, white, func(c *canvas) { c.circle(21.25, 12.75, 2.475) })
	// prompt chevron
	lilac := image.NewUniform(color.RGBA{0xc7, 0xd2, 0xfe, 255})
	pts := [][2]float32{{7, 9}, {11, 12}, {7, 15}}
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		fill(img, size, lilac, func(c *canvas) { c.stroke(2, a, b) })
	}
	for _, p := range pts {
		fill(img, size, lilac, func(c *canvas) { c.circle(p[0], p[1], 1) })
	}
	return img
}

func save(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func main() {
	out := flag.String("out", "build/icon", "output directory")
	flag.Parse()
	set := filepath.Join(*out, "devknife.iconset")
	if err := os.MkdirAll(set, 0o755); err != nil {
		panic(err)
	}
	master := render(1024)
	if err := save(filepath.Join(*out, "icon-1024.png"), master); err != nil {
		panic(err)
	}
	for _, s := range []int{16, 32, 64, 128, 256, 512, 1024} {
		img := render(s) // render each size natively for crisp edges
		if s <= 512 {
			_ = save(filepath.Join(set, fmt.Sprintf("icon_%dx%d.png", s, s)), img)
		}
		if s >= 32 {
			_ = save(filepath.Join(set, fmt.Sprintf("icon_%dx%d@2x.png", s/2, s/2)), img)
		}
	}
	fmt.Println(filepath.Join(*out, "icon-1024.png"))
}
