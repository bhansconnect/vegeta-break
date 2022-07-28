package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"runtime"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	ct "github.com/daviddengcn/go-colortext"
	vegeta "github.com/tsenart/vegeta/lib"
)

func usage() {
	fmt.Printf("Usage: %s [OPTIONS] url\n", os.Args[0])
	flag.PrintDefaults()
}

func testRate(rps int, sla, duration, maxTimeout time.Duration, maxConnections int, percentile float64, url, method string, body []byte, keepAlive bool) bool {
	target := vegeta.Target{
		Method: method,
		URL:    url,
		Body:   body,
		Header: make(map[string][]string),
	}
	target.Header.Add("Accept-Encoding", "gzip, deflate")
	targeter := vegeta.NewStaticTargeter(target)
	attacker := vegeta.NewAttacker(vegeta.Timeout(maxTimeout), vegeta.KeepAlive(keepAlive), vegeta.Connections(maxConnections))
	metrics := vegeta.Metrics{}

	hist := hdrhistogram.New(1, 3600000, 3)

	fmt.Printf("%s Starting %d req/sec Load Test for %s...\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, duration)
	vrate := vegeta.Rate{Freq: rps, Per: time.Second}
	for res := range attacker.Attack(targeter, vrate, duration, "Latency Test") {
		metrics.Add(res)
		hist.RecordValue(res.Latency.Nanoseconds() / 1e3)
	}
	metrics.Close()

	var buff bytes.Buffer
	if _, err := hist.PercentilesPrint(&buff, 10, 1.0); err != nil {
		panic(err)
	}
	if err := ioutil.WriteFile(fmt.Sprintf("lat_%d.txt", rps), buff.Bytes(), 0644); err != nil {
		panic(err)
	}

	attacker = nil
	targeter = nil

	latency := time.Duration(hist.ValueAtQuantile(percentile)) * time.Microsecond
	if 100*metrics.Success < percentile {
		ct.Foreground(ct.Red, false)
		fmt.Printf("%s Failed at %d req/sec (errors: %f%%)\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, 100*(1-metrics.Success))
		ct.Foreground(ct.White, false)
		return false
	}
	if metrics.Rate < float64(rps) && float64(rps)-metrics.Rate > 1 {
		ct.Foreground(ct.Red, false)
		fmt.Printf("%s Failed at %d req/sec (Only managed to get to %f req/sec)\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, metrics.Rate)
		ct.Foreground(ct.White, false)
		return false
	}
	if latency > sla {
		ct.Foreground(ct.Red, false)
		fmt.Printf("%s Failed at %d req/sec (latency %s)\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, latency)
		ct.Foreground(ct.White, false)
		return false
	}
	ct.Foreground(ct.Green, false)
	fmt.Printf("%s Success at %d req/sec (latency %s)\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, latency)
	ct.Foreground(ct.White, false)
	return true
}

func main() {
	flag.Usage = usage
	var duration time.Duration
	var climbMultiple float64
	var percentile float64
	var rpsAccuracy float64
	var rps int
	var sla time.Duration
	var maxTimeout time.Duration
	var maxConnections int
	var keepAlive bool
	var bodyFile string
	var method string
	flag.IntVar(&rps, "rps", 20, "Starting requests per second")
	flag.DurationVar(&sla, "sla", 500*time.Millisecond, "Max acceptable latency")
	flag.DurationVar(&duration, "duration", time.Minute, "Duration for each latency test")
	flag.DurationVar(&maxTimeout, "max-timeout", 3*time.Second, "Max time to wait before a response")
	flag.IntVar(&maxConnections, "max-connections", 10000, "Max open idle connections per target host")
	flag.BoolVar(&keepAlive, "keep-alive", true, "whether or not to use http keep alive connections")
	flag.Float64Var(&percentile, "percentile", 99.9, "The percentile that latency is measured at")
	flag.Float64Var(&rpsAccuracy, "rps-accuracy", 100, "How close the output should be to the correct rps. 100 is exact rps. 95 would be within 5%")
	flag.Float64Var(&climbMultiple, "climb-multiple", 2.0, "How many times more requests to send after a success. Must be greater than 1.0")
	flag.StringVar(&bodyFile, "body-file", "", "a file to be read and used as the body of each request")
	flag.StringVar(&method, "method", "GET", "the http request method")
	flag.Parse()
	if flag.NArg() == 0 || rps <= 0 || maxConnections <= 0 || climbMultiple <= 1.0 || percentile < 0 || percentile > 100 || rpsAccuracy > 100 || rpsAccuracy <= 0 {
		flag.Usage()
		os.Exit(1)
	}
	var body []byte
	if bodyFile != "" {
		bytes, err := ioutil.ReadFile(bodyFile)
		if err != nil {
			fmt.Printf("Failed to load body from file %q\n", bodyFile)
			os.Exit(1)
		}
		body = bytes
	}
	url := flag.Arg(0)

	okRate := 1
	var nokRate int
	// first, find the point at which the system breaks
	for {
		if testRate(rps, sla, duration, maxTimeout, maxConnections, percentile, url, method, body, keepAlive) {
			okRate = rps
			rps = int(math.Ceil(float64(rps) * climbMultiple))
		} else {
			nokRate = rps
			break
		}
		runtime.GC()
	}

	// next, do a binary search between okRate and nokRate
	rpsAccuracy = rpsAccuracy / 100.0
	for float64(okRate)/float64(nokRate-1) < rpsAccuracy {
		rps = (nokRate + okRate) / 2
		if testRate(rps, sla, duration, maxTimeout, maxConnections, percentile, url, method, body, keepAlive) {
			okRate = rps
		} else {
			nokRate = rps
		}
		runtime.GC()
	}
	if nokRate-1 == okRate {
		fmt.Printf("Maximum Working Rate: %d req/sec\n", okRate)
	} else {
		fmt.Printf("Maximum Working Rate Within: %d-%d req/sec\n", okRate, (nokRate - 1))
	}
	os.Rename(fmt.Sprintf("lat_%d.txt", okRate), fmt.Sprintf("lat_%d_best.txt", okRate))
}
