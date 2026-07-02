package editor

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"sort"
	"strconv"
)

// Sixel encoder. Unlike kitty/iTerm2 (which ship the PNG bytes verbatim), sixel
// is a palette raster format, so we quantize the image ourselves: box-average
// downscale to a bounded size, Floyd–Steinberg dither onto the 256-color Plan9
// palette (both stdlib), then emit the DCS sixel stream. Each "sixel" byte
// encodes six vertical pixels of one color; a band is drawn one color at a time,
// overlaid with `$` (carriage return) and advanced with `-` (newline).

// sixelEncode returns a complete DCS sixel sequence for img, downscaled so its
// width is at most maxW pixels (never upscaled). maxW bounds both the on-screen
// size and the encode cost.
func sixelEncode(img image.Image, maxW int) []byte {
	src := downscaleToWidth(img, maxW)
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return nil
	}

	// quantize to a paletted image with dithering (origin 0,0 so Pix indexes cleanly).
	pal := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
	draw.FloydSteinberg.Draw(pal, pal.Bounds(), src, b.Min)

	var out bytes.Buffer
	out.WriteString("\x1bPq")                          // DCS ... q (default params)
	out.WriteString(`"1;1;` + itoa(w) + `;` + itoa(h)) // raster: 1:1 aspect, W×H
	for i, c := range pal.Palette {                    // define color registers (RGB in 0..100)
		r, g, bl, _ := c.RGBA()
		out.WriteString("#" + itoa(i) + ";2;" +
			itoa(int(r)*100/0xffff) + ";" + itoa(int(g)*100/0xffff) + ";" + itoa(int(bl)*100/0xffff))
	}

	nbands := (h + 5) / 6
	for band := 0; band < nbands; band++ {
		y0 := band * 6
		// which palette indices appear in this 6-row band
		present := map[int]bool{}
		for row := 0; row < 6 && y0+row < h; row++ {
			y := y0 + row
			for x := 0; x < w; x++ {
				present[int(pal.Pix[y*pal.Stride+x])] = true
			}
		}
		colors := make([]int, 0, len(present))
		for ci := range present {
			colors = append(colors, ci)
		}
		sort.Ints(colors) // deterministic output

		for idx, ci := range colors {
			if idx > 0 {
				out.WriteByte('$') // graphics CR: overlay the next color on the same band
			}
			out.WriteString("#" + itoa(ci))
			// build this color's row of sixel bytes across the width, run-length encoded
			var prev byte
			run := 0
			flush := func() {
				if run == 0 {
					return
				}
				if run > 3 {
					out.WriteByte('!')
					out.WriteString(itoa(run))
					out.WriteByte(prev)
				} else {
					for k := 0; k < run; k++ {
						out.WriteByte(prev)
					}
				}
				run = 0
			}
			for x := 0; x < w; x++ {
				var bits byte
				for row := 0; row < 6; row++ {
					y := y0 + row
					if y < h && int(pal.Pix[y*pal.Stride+x]) == ci {
						bits |= 1 << row
					}
				}
				sx := bits + 0x3f // sixel bytes start at '?'
				if run > 0 && sx == prev {
					run++
				} else {
					flush()
					prev = sx
					run = 1
				}
			}
			flush()
		}
		out.WriteByte('-') // graphics NL: advance six pixels down
	}
	out.WriteString("\x1b\\") // ST
	return out.Bytes()
}

func itoa(n int) string { return strconv.Itoa(n) }

// downscaleToWidth box-averages src down to at most maxW pixels wide, preserving
// aspect. src is returned unchanged when it already fits or maxW is non-positive.
func downscaleToWidth(src image.Image, maxW int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxW <= 0 || w <= maxW {
		return src
	}
	nw := maxW
	nh := h * maxW / w
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for dy := 0; dy < nh; dy++ {
		sy0 := b.Min.Y + dy*h/nh
		sy1 := b.Min.Y + (dy+1)*h/nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < nw; dx++ {
			sx0 := b.Min.X + dx*w/nw
			sx1 := b.Min.X + (dx+1)*w/nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rr, gg, bb, aa, cnt uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, bl, a := src.At(sx, sy).RGBA()
					rr += uint64(r)
					gg += uint64(g)
					bb += uint64(bl)
					aa += uint64(a)
					cnt++
				}
			}
			if cnt == 0 {
				cnt = 1
			}
			dst.Set(dx, dy, color.RGBA64{
				R: uint16(rr / cnt), G: uint16(gg / cnt), B: uint16(bb / cnt), A: uint16(aa / cnt),
			})
		}
	}
	return dst
}
