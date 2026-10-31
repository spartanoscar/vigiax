package station

import (
	"math"
	"sort"
	"time"

	"github.com/spartanoscar/vigiax/server/internal/alert"
)

// smoothSpan bounds the median: three frames of the twin (5 s apart), but a
// single photo of an hourly camera, where confirm_readings does the work.
const smoothSpan = 2 * time.Minute

// A jump is a level change between two frames close in time that water does
// not make at that pace: the twin's fastest flood moves about 20 cm a frame.
const (
	jumpCm     = 50.0
	jumpWithin = time.Minute
)

// Observe turns the latest reading into what the rules see: the median of the
// last three levels (one odd frame cannot move it) and the rise rate, both
// over the readings after the last jump, so a camera that moved does not
// count as rising water.
func (st *Station) Observe(c *alert.Config, rd Reading, since time.Time) alert.Obs {
	o := alert.Obs{T: rd.T, Blind: rd.Blind}
	if rd.Blind || rd.LevelCm == nil {
		o.Blind = true
		return o
	}
	window := time.Duration(c.RiseWindowMin * float64(time.Minute))
	var rs []Reading
	for _, r := range st.Readings(rd.T.Add(-window)) {
		if r.LevelCm != nil && !r.T.After(rd.T) && !r.T.Before(since) {
			rs = append(rs, r)
		}
	}
	from := 0
	for i := 1; i < len(rs); i++ {
		if d := *rs[i].LevelCm - *rs[i-1].LevelCm; math.Abs(d) >= jumpCm && rs[i].T.Sub(rs[i-1].T) <= jumpWithin {
			from = i
			if i == len(rs)-1 {
				o.JumpCm = &d
			}
		}
	}
	rs = rs[from:]
	var lv []float64
	for i := len(rs) - 1; i >= 0 && len(lv) < 3 && rd.T.Sub(rs[i].T) <= smoothSpan; i-- {
		lv = append(lv, *rs[i].LevelCm)
	}
	if len(lv) == 0 { // the reading itself is always there; guard anyway
		lv = []float64{*rd.LevelCm}
	}
	sort.Float64s(lv)
	m := lv[len(lv)/2]
	o.LevelCm, o.LastCm = &m, rd.LevelCm
	if v, ok := slope(rs, window); ok {
		o.RiseCmPerMin = &v
	}
	return o
}

// slope is the least-squares rise in cm per minute, with the same demands as
// Rise: three readings covering at least a third of the window.
func slope(rs []Reading, window time.Duration) (float64, bool) {
	if len(rs) < 3 || rs[len(rs)-1].T.Sub(rs[0].T) < window/3 {
		return 0, false
	}
	var st, sl, stt, stl float64
	for _, r := range rs {
		t := r.T.Sub(rs[0].T).Minutes()
		st, sl, stt, stl = st+t, sl+*r.LevelCm, stt+t*t, stl+t**r.LevelCm
	}
	n := float64(len(rs))
	den := n*stt - st*st
	if den == 0 {
		return 0, false
	}
	return (n*stl - st*sl) / den, true
}
