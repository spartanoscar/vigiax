// Package usgs reads U.S. Geological Survey webcams (HIVIS/NIMS) and gage
// heights. Imagery and data are public domain; credit "U.S. Geological Survey".
package usgs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// FrameURL is the public 720p image of a webcam file.
func FrameURL(cam, file string) string {
	return "https://usgs-nims-images.s3.amazonaws.com/720/" + cam + "/" + file
}

// FrameTime is the capture time encoded in a file name such as
// OK_North_Canadian_River_near_El_Reno___2026-10-27T23-00-05Z.jpg.
func FrameTime(file string) (time.Time, error) {
	_, stamp, ok := strings.Cut(file, "___")
	if !ok {
		return time.Time{}, fmt.Errorf("usgs: no time in %q", file)
	}
	return time.Parse("2006-01-02T15-04-05Z", strings.TrimSuffix(stamp, ".jpg"))
}

// Files lists the webcam images taken in (after, before), oldest first.
func Files(ctx context.Context, cam string, after, before time.Time) ([]string, error) {
	q := url.Values{"camId": {cam}, "after": {after.UTC().Format(time.RFC3339)}, "before": {before.UTC().Format(time.RFC3339)},
		"limit": {"2000"}, "recent": {"false"}}
	b, err := Get(ctx, "https://api.waterdata.usgs.gov/nims/v0/listFiles?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var files []string
	if err := json.Unmarshal(b, &files); err != nil {
		return nil, fmt.Errorf("usgs: listFiles: %w", err)
	}
	sort.Strings(files) // names end in the capture time
	return files, nil
}

// GageHeights returns the gage height readings (ft) of a site in [t0, t1].
func GageHeights(ctx context.Context, site string, t0, t1 time.Time) (map[time.Time]float64, error) {
	q := url.Values{"format": {"json"}, "sites": {site}, "parameterCd": {"00065"},
		"startDT": {t0.Format("2006-01-02T15:04-07:00")}, "endDT": {t1.Format("2006-01-02T15:04-07:00")}}
	b, err := Get(ctx, "https://waterservices.usgs.gov/nwis/iv/?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var res struct {
		Value struct {
			TimeSeries []struct {
				Values []struct {
					Value []struct{ Value, DateTime string }
				}
			}
		}
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("usgs: gage heights: %w", err)
	}
	out := map[time.Time]float64{}
	if len(res.Value.TimeSeries) == 0 || len(res.Value.TimeSeries[0].Values) == 0 {
		return out, nil
	}
	for _, v := range res.Value.TimeSeries[0].Values[0].Value {
		t, err1 := time.Parse(time.RFC3339, v.DateTime)
		var x float64
		_, err2 := fmt.Sscan(v.Value, &x)
		if err1 == nil && err2 == nil && x > -999 {
			out[t] = x
		}
	}
	return out, nil
}

// Nearest is the gage reading closest to t, if one lies within the given distance.
func Nearest(g map[time.Time]float64, t time.Time, within time.Duration) (float64, bool) {
	best, bestD := 0.0, within+1
	for gt, v := range g {
		if d := gt.Sub(t).Abs(); d < bestD {
			best, bestD = v, d
		}
	}
	return best, bestD <= within
}

// Get fetches a URL and fails on any status other than 200.
func Get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usgs: GET %s: %s", u, res.Status)
	}
	return io.ReadAll(res.Body)
}
