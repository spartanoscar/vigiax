// Mask decoding for SAM 3.1 "one_bit" payloads.
//
// Go port of the one_bit decoder in Meta's meta-sam-parser 0.1.0
// (https://pypi.org/project/meta-sam-parser/), Copyright (c) Meta Platforms, Inc.
// and affiliates, distributed under the SAM License (see LICENSE-SAM in this
// directory). Format: a base85 length-prefixed byte string holding a binary
// range-coded raster, each pixel modeled on 12 causal neighbours.

package sam

import (
	"errors"
	"fmt"
)

const (
	radix        = 85
	prefixLength = 5
	contextCount = 1 << 12
	padding      = 2
	countInc     = 14
	countLimit   = 4096
	rangeTop     = 1 << 24
	rangeBottom  = 1 << 16
)

var digitValue = func() [128]int {
	var v [128]int
	for i := range v {
		v[i] = -1
	}
	n := 0
	for c := 0x21; c < 0x7f; c++ {
		switch c {
		case '"', '\\', ',', ';', '<', '>', '|':
			continue
		}
		v[c] = n
		n++
	}
	return v
}()

var errMask = errors.New("sam: invalid mask payload")

func digit(c byte) (uint64, error) {
	if c >= 128 || digitValue[c] < 0 || digitValue[c] >= radix {
		return 0, errMask
	}
	return uint64(digitValue[c]), nil
}

// unpack decodes the base85 payload (without the encoding marker).
func unpack(p string) ([]byte, error) {
	if len(p) < prefixLength {
		return nil, errMask
	}
	var n uint64
	for i := 0; i < prefixLength; i++ {
		d, err := digit(p[i])
		if err != nil {
			return nil, err
		}
		n = n*radix + d
	}
	rem := int(n % 4)
	want := prefixLength + int(n/4)*5
	if rem > 0 {
		want += rem + 1
	}
	if len(p) != want {
		return nil, fmt.Errorf("%w: length %d, prefix says %d", errMask, len(p), want)
	}
	out := make([]byte, n)
	src, dst := prefixLength, 0
	for full := int(n) - rem; dst < full; dst += 4 {
		var v uint64
		for i := 0; i < 5; i++ {
			d, err := digit(p[src+i])
			if err != nil {
				return nil, err
			}
			v = v*radix + d
		}
		if v > 0xffffffff {
			return nil, errMask
		}
		src += 5
		out[dst], out[dst+1], out[dst+2], out[dst+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	if rem > 0 {
		var v uint64
		for i := 0; i < 5; i++ {
			d := uint64(radix - 1)
			if i < rem+1 {
				var err error
				if d, err = digit(p[src+i]); err != nil {
					return nil, err
				}
			}
			v = v*radix + d
		}
		if v > 0xffffffff {
			return nil, errMask
		}
		for i := 0; i < rem; i++ {
			out[dst+i] = byte(v >> (24 - 8*i))
		}
	}
	return out, nil
}

type rangeDecoder struct {
	in                 []byte
	pos                int
	low, rng, code, sc uint32
}

func newRangeDecoder(in []byte) (*rangeDecoder, error) {
	if len(in) < 4 {
		return nil, errMask
	}
	d := &rangeDecoder{in: in, rng: 0xffffffff}
	for i := 0; i < 4; i++ {
		b, err := d.read()
		if err != nil {
			return nil, err
		}
		d.code = d.code<<8 | uint32(b)
	}
	return d, nil
}

func (d *rangeDecoder) read() (byte, error) {
	if d.pos >= len(d.in) {
		return 0, errMask
	}
	b := d.in[d.pos]
	d.pos++
	return b, nil
}

func (d *rangeDecoder) frequency(total uint32) (uint32, error) {
	d.sc = d.rng / total
	if d.sc == 0 {
		return 0, errMask
	}
	v := (d.code - d.low) / d.sc
	if v >= total {
		v = total - 1
	}
	return v, nil
}

func (d *rangeDecoder) decode(cum, freq uint32) error {
	d.low += d.sc * cum
	d.rng = d.sc * freq
	for {
		if d.low^(d.low+d.rng) < rangeTop {
		} else if d.rng < rangeBottom {
			d.rng = -d.low & (rangeBottom - 1)
		} else {
			return nil
		}
		b, err := d.read()
		if err != nil {
			return err
		}
		d.code = d.code<<8 | uint32(b)
		d.low <<= 8
		d.rng <<= 8
	}
}

// DecodeOneBit returns a row-major raster of 0/1 values, h rows × w columns.
// payload starts with the "!" marker.
func DecodeOneBit(payload string, w, h int) ([]byte, error) {
	if len(payload) == 0 || payload[0] != '!' {
		return nil, fmt.Errorf("%w: not a one_bit payload", errMask)
	}
	if w <= 0 || h <= 0 {
		return nil, errMask
	}
	packed, err := unpack(payload[1:])
	if err != nil {
		return nil, err
	}
	d, err := newRangeDecoder(packed)
	if err != nil {
		return nil, err
	}
	counts := make([]uint16, contextCount*2)
	for i := range counts {
		counts[i] = 1
	}
	pw := w + 2*padding
	padded := make([]byte, pw*(h+padding))
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		row := (y+padding)*pw + padding
		for x := 0; x < w; x++ {
			p := row + x
			prior, earlier := p-pw, p-2*pw
			ctx := int(padded[p-1]) |
				int(padded[p-2])<<1 |
				int(padded[prior-2])<<2 |
				int(padded[prior-1])<<3 |
				int(padded[prior])<<4 |
				int(padded[prior+1])<<5 |
				int(padded[prior+2])<<6 |
				int(padded[earlier-2])<<7 |
				int(padded[earlier-1])<<8 |
				int(padded[earlier])<<9 |
				int(padded[earlier+1])<<10 |
				int(padded[earlier+2])<<11
			off := ctx << 1
			zero, one := uint32(counts[off]), uint32(counts[off+1])
			v, err := d.frequency(zero + one)
			if err != nil {
				return nil, err
			}
			var bit byte
			if v < zero {
				err = d.decode(0, zero)
			} else {
				bit = 1
				err = d.decode(zero, one)
			}
			if err != nil {
				return nil, err
			}
			padded[p] = bit
			out[y*w+x] = bit
			counts[off+int(bit)] += countInc
			if counts[off]+counts[off+1] >= countLimit {
				counts[off] = max(counts[off]>>1, 1)
				counts[off+1] = max(counts[off+1]>>1, 1)
			}
		}
	}
	return out, nil
}
