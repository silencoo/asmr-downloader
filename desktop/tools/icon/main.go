package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var iconSizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

type iconEntry struct {
	size int
	data []byte
}

func main() {
	input := flag.String("input", "build/appicon.png", "source PNG")
	output := flag.String("output", "build/windows/icon.ico", "target ICO")
	flag.Parse()

	sourceFile, err := os.Open(*input)
	if err != nil {
		fatal(err)
	}
	source, err := png.Decode(sourceFile)
	sourceFile.Close()
	if err != nil {
		fatal(err)
	}

	entries := make([]iconEntry, 0, len(iconSizes))
	for _, size := range iconSizes {
		resized := resizeOpaque(source, size, color.NRGBA{R: 230, G: 83, B: 139, A: 255})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, resized); err != nil {
			fatal(err)
		}
		entries = append(entries, iconEntry{size: size, data: encoded.Bytes()})
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		fatal(err)
	}
	target, err := os.Create(*output)
	if err != nil {
		fatal(err)
	}
	if err := writeICO(target, entries); err != nil {
		target.Close()
		fatal(err)
	}
	if err := target.Close(); err != nil {
		fatal(err)
	}
	fmt.Printf("Generated opaque Windows icon: %s\n", *output)
}

func writeICO(target *os.File, entries []iconEntry) error {
	if err := binary.Write(target, binary.LittleEndian, uint16(0)); err != nil {
		return err
	}
	if err := binary.Write(target, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(target, binary.LittleEndian, uint16(len(entries))); err != nil {
		return err
	}

	offset := uint32(6 + len(entries)*16)
	for _, entry := range entries {
		dimension := byte(entry.size)
		if entry.size == 256 {
			dimension = 0
		}
		header := []byte{dimension, dimension, 0, 0}
		if _, err := target.Write(header); err != nil {
			return err
		}
		if err := binary.Write(target, binary.LittleEndian, uint16(1)); err != nil {
			return err
		}
		if err := binary.Write(target, binary.LittleEndian, uint16(32)); err != nil {
			return err
		}
		if err := binary.Write(target, binary.LittleEndian, uint32(len(entry.data))); err != nil {
			return err
		}
		if err := binary.Write(target, binary.LittleEndian, offset); err != nil {
			return err
		}
		offset += uint32(len(entry.data))
	}

	for _, entry := range entries {
		if _, err := target.Write(entry.data); err != nil {
			return err
		}
	}
	return nil
}

func resizeOpaque(source image.Image, size int, background color.NRGBA) *image.NRGBA {
	bounds := source.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	target := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		sourceY := (float64(y)+0.5)*float64(height)/float64(size) - 0.5
		y0, y1, fy := sampleAxis(sourceY, height)
		for x := 0; x < size; x++ {
			sourceX := (float64(x)+0.5)*float64(width)/float64(size) - 0.5
			x0, x1, fx := sampleAxis(sourceX, width)
			topLeft := flatten(source.At(bounds.Min.X+x0, bounds.Min.Y+y0), background)
			topRight := flatten(source.At(bounds.Min.X+x1, bounds.Min.Y+y0), background)
			bottomLeft := flatten(source.At(bounds.Min.X+x0, bounds.Min.Y+y1), background)
			bottomRight := flatten(source.At(bounds.Min.X+x1, bounds.Min.Y+y1), background)
			target.SetNRGBA(x, y, bilinear(topLeft, topRight, bottomLeft, bottomRight, fx, fy))
		}
	}
	return target
}

func sampleAxis(position float64, limit int) (int, int, float64) {
	position = math.Max(0, math.Min(float64(limit-1), position))
	first := int(math.Floor(position))
	second := min(first+1, limit-1)
	return first, second, position - float64(first)
}

func flatten(value color.Color, background color.NRGBA) color.NRGBA {
	source := color.NRGBAModel.Convert(value).(color.NRGBA)
	alpha := uint32(source.A)
	inverse := uint32(255 - source.A)
	return color.NRGBA{
		R: uint8((uint32(source.R)*alpha + uint32(background.R)*inverse + 127) / 255),
		G: uint8((uint32(source.G)*alpha + uint32(background.G)*inverse + 127) / 255),
		B: uint8((uint32(source.B)*alpha + uint32(background.B)*inverse + 127) / 255),
		A: 255,
	}
}

func bilinear(a, b, c, d color.NRGBA, fx, fy float64) color.NRGBA {
	channel := func(av, bv, cv, dv uint8) uint8 {
		top := float64(av)*(1-fx) + float64(bv)*fx
		bottom := float64(cv)*(1-fx) + float64(dv)*fx
		return uint8(math.Round(top*(1-fy) + bottom*fy))
	}
	return color.NRGBA{
		R: channel(a.R, b.R, c.R, d.R),
		G: channel(a.G, b.G, c.G, d.G),
		B: channel(a.B, b.B, c.B, d.B),
		A: 255,
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
