package main

import (
	"../util"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bitly/go-simplejson"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type GraphInterval struct {
	Selected   bool
	Timeframe  string        // the UI string
	GraphFrom  string        // ?from=.
	GraphUntil string        // ?until=.
	Duration   time.Duration // for sort order
}

type GraphIntervals []*GraphInterval

type GraphOptions struct {
	Configured        bool
	Enabled           bool
	GraphiteUrl       string
	UseStatsdPrefix   bool
	TimeframeString   template.URL
	AllGraphIntervals []*GraphInterval
	GraphInterval     *GraphInterval
}

func NewGraphOptions(rw http.ResponseWriter, req *http.Request, r *util.ReqParams) *GraphOptions {
	selectedTimeString, err := r.Get("t")
	if err != nil && selectedTimeString == "" {
		// get from cookie
		cookie, err := req.Cookie("t")
		if err != nil {
			selectedTimeString = "2h"
		} else {
			selectedTimeString = cookie.Value
		}
	} else {
		// set cookie
		cookie := &http.Cookie{
			Name:     "t",
			Value:    selectedTimeString,
			Path:     "/",
			Domain:   req.Host,
			Expires:  time.Now().Add(time.Duration(720) * time.Hour),
			HttpOnly: true,
		}
		http.SetCookie(rw, cookie)
	}
	g, err := GraphIntervalForTimeframe(selectedTimeString, true)
	if err != nil {
		g, _ = GraphIntervalForTimeframe("2h", true)
	}
	base := *graphiteUrl
	configured := base != ""
	enabled := configured
	if *proxyGraphite {
		base = ""
	}
	if g.Timeframe == "off" {
		enabled = false
	}
	o := &GraphOptions{
		Configured:        configured,
		Enabled:           enabled,
		UseStatsdPrefix:   *useStatsdPrefixes,
		GraphiteUrl:       base,
		AllGraphIntervals: DefaultGraphTimeframes(selectedTimeString),
		GraphInterval:     g,
	}
	return o
}

func (g *GraphInterval) UrlOption() template.URL {
	return template.URL(fmt.Sprintf("t=%s", g.Timeframe))
}

func DefaultGraphTimeframes(selected string) GraphIntervals {
	var d GraphIntervals

	for _, t := range []string{"1h", "2h", "12h", "24h", "48h", "168h", "off"} {
		g, err := GraphIntervalForTimeframe(t, t == selected)
		if err != nil {
			log.Fatalf("error parsing duration %s", err.Error())
		}
		d = append(d, g)
	}
	return d
}

func GraphIntervalForTimeframe(t string, selected bool) (*GraphInterval, error) {
	if t == "off" {
		return &GraphInterval{
			Selected:   selected,
			Timeframe:  t,
			GraphFrom:  "",
			GraphUntil: "",
			Duration:   0,
		}, nil
	}
	duration, err := time.ParseDuration(t)
	if err != nil {
		return nil, err
	}
	start, end := startEndForTimeframe(duration)
	return &GraphInterval{
		Selected:   selected,
		Timeframe:  t,
		GraphFrom:  start,
		GraphUntil: end,
		Duration:   duration,
	}, nil
}

func (g *GraphOptions) Prefix(metricType string) string {
	if g.UseStatsdPrefix && metricType == "counter" {
		return "stats_counts."
	} else if g.UseStatsdPrefix && metricType == "gauge" {
		return "stats.gauges."
	}
	return ""
}

func startEndForTimeframe(t time.Duration) (string, string) {
	start := fmt.Sprintf("-%dmin", int(t.Minutes()))
	return start, "-1min"
}

func (t *Topic) Target(g *GraphOptions, key string) (string, string) {
	color := "blue"
	if key == "depth" || key == "deferred_count" {
		color = "red"
	}
	target := fmt.Sprintf("%snsq.*.topic.%s.%s", g.Prefix(metricType(key)), t.TopicName, key)
	return target, color
}
func (t *Topic) Sparkline(g *GraphOptions, key string) template.URL {
	target, color := t.Target(g, key)
	return g.Sparkline(target, color)
}

func (t *Topic) LargeGraph(g *GraphOptions, key string) template.URL {
	target, color := t.Target(g, key)
	return g.LargeGraph(key, target, color)
}

func (t *Topic) Rate(g *GraphOptions) string {
	target, _ := t.Target(g, "message_count")
	return target
}

func (t *TopicHostStats) Target(g *GraphOptions, key string) (string, string) {
	h := graphiteHostKey(t.HostAddress)
	if t.Aggregate {
		h = "*"
	}
	color := "blue"
	if key == "depth" || key == "deferred_count" {
		color = "red"
	}
	target := fmt.Sprintf("%snsq.%s.topic.%s.%s", g.Prefix(metricType(key)), h, t.Topic, key)
	return target, color
}
func (t *TopicHostStats) Sparkline(g *GraphOptions, key string) template.URL {
	target, color := t.Target(g, key)
	return g.Sparkline(target, color)
}
func (t *TopicHostStats) LargeGraph(g *GraphOptions, key string) template.URL {
	target, color := t.Target(g, key)
	return g.LargeGraph(key, target, color)
}
func (t *TopicHostStats) Rate(g *GraphOptions) string {
	target, _ := t.Target(g, "message_count")
	return target
}

func metricType(key string) string {
	metricType := "counter"
	switch key {
	case "backend_depth", "depth", "clients", "in_flight_count":
		metricType = "gauge"
	}
	return metricType
}

func (c *ChannelStats) Target(g *GraphOptions, key string) (string, string) {
	h := "*"
	if len(c.HostStats) == 0 {
		h = graphiteHostKey(c.HostAddress)
	}
	color := "blue"
	if key == "depth" || key == "deferred_count" {
		color = "red"
	}
	target := fmt.Sprintf("%snsq.%s.topic.%s.channel.%s.%s", g.Prefix(metricType(key)), h, c.Topic, c.ChannelName, key)
	return target, color
}
func (c *ChannelStats) Sparkline(g *GraphOptions, key string) template.URL {
	target, color := c.Target(g, key)
	return g.Sparkline(target, color)
}
func (c *ChannelStats) LargeGraph(g *GraphOptions, key string) template.URL {
	target, color := c.Target(g, key)
	return g.LargeGraph(key, target, color)
}
func (t *ChannelStats) Rate(g *GraphOptions) string {
	target, _ := t.Target(g, "message_count")
	return target
}

func (g *GraphOptions) Sparkline(target string, color string) template.URL {
	params := url.Values{}
	params.Set("height", "20")
	params.Set("width", "120")
	params.Set("hideGrid", "true")
	params.Set("hideLegend", "true")
	params.Set("hideAxes", "true")
	params.Set("bgcolor", "ff000000") // transparent
	params.Set("fgcolor", "black")
	params.Set("margin", "0")
	params.Set("colorList", color)
	params.Set("yMin", "0")
	params.Set("target", fmt.Sprintf("sumSeries(%s)", target))
	params.Set("from", g.GraphInterval.GraphFrom)
	params.Set("until", g.GraphInterval.GraphUntil)
	return template.URL(fmt.Sprintf("%s/render?%s", g.GraphiteUrl, params.Encode()))
}

func (g *GraphOptions) LargeGraph(key string, target string, color string) template.URL {
	params := url.Values{}
	params.Set("height", "450")
	params.Set("width", "800")
	params.Set("bgcolor", "ff000000") // transparent
	params.Set("fgcolor", "999999")
	params.Set("colorList", color)
	params.Set("yMin", "0")
	target = fmt.Sprintf(`summarize(sumSeries(%s),"1min","avg")`, target)
	if metricType(key) != "gauge" {
		target = fmt.Sprintf(`scaleToSeconds(%s,1)`, target)
	}
	params.Set("target", target)
	params.Set("from", g.GraphInterval.GraphFrom)
	params.Set("until", g.GraphInterval.GraphUntil)
	return template.URL(fmt.Sprintf("%s/render?%s", g.GraphiteUrl, params.Encode()))
}

func rateQuery(target string) string {
	params := url.Values{}
	params.Set("from", "-2min")
	params.Set("until", "-1min")
	params.Set("format", "json")
	params.Set("target", fmt.Sprintf("sumSeries(%s)", target))
	return fmt.Sprintf("/render?%s", params.Encode())
}

func parseRateResponse(body []byte) (string, error) {
	js, err := simplejson.NewJson([]byte(body))
	if err != nil {
		log.Printf("ERROR: failed to parse metadata - %s", err.Error())
		return "", err
	}

	js, ok := js.GetIndex(0).CheckGet("datapoints")
	if !ok {
		return "", errors.New("datapoints not found")
	}

	rate, err := js.GetIndex(0).GetIndex(0).Float64()
	rate_str := fmt.Sprintf("%.2f", rate/60)
	response := map[string]string{"datapoint": rate_str}
	byte_response, err := json.Marshal(response)
	if err != nil {
		return "", errors.New("marshal failed")
	}

	return string(byte_response), nil
}

func graphiteHostKey(h string) string {
	s := strings.Replace(h, ".", "_", -1)
	return strings.Replace(s, ":", "_", -1)
}
