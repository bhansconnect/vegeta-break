package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/codahale/hdrhistogram"
	vegeta "github.com/tsenart/vegeta/lib"
	histwriter "github.com/tylertreat/hdrhistogram-writer"
)

func usage() {
	fmt.Printf("Usage: %s [OPTIONS] url\n", os.Args[0])
	flag.PrintDefaults()
}

func testRate(rps int, sla time.Duration, duration time.Duration, percentile float64, scaleupDuration time.Duration, scaleupSteps int, url string) bool {
	target := vegeta.Target{
		Method: "GET",
		URL:    url,
		Header: make(map[string][]string),
	}
	target.Header.Add("Accept-Encoding", "gzip, deflate")
	targeter := vegeta.NewStaticTargeter(target)
	attacker := vegeta.NewAttacker()

	hist := hdrhistogram.New(1, 3600000, 3)

	scaleupRate := float64(rps) / float64(scaleupSteps)
	subDuration := scaleupDuration / time.Duration(scaleupSteps)
	fmt.Printf("Starting %d req/sec Scaleup for %s...\n", rps, scaleupDuration)
	for i := 0; i < scaleupSteps; i++ {
		r := int(math.Ceil(float64(i+1) * scaleupRate))
		if r > rps {
			r = rps
		}
		vrate := vegeta.Rate{Freq: r, Per: time.Second}
		for range attacker.Attack(targeter, vrate, subDuration, "Scale Up") {
		}
	}
	fmt.Printf("Starting %d req/sec Load Test for %s...\n", rps, duration)
	vrate := vegeta.Rate{Freq: rps, Per: time.Second}
	var errors int64
	for res := range attacker.Attack(targeter, vrate, duration, "Latency Test") {
		if res.Code <= 400 {
			hist.RecordValue(res.Latency.Nanoseconds() / 1e3)
		} else {
			errors++
		}
	}

	file, err := os.OpenFile(fmt.Sprintf("lat_%d.txt", rps), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	histwriter.WriteDistribution(hist, histwriter.Logarithmic, 1e-3, file)

	//adjust percentile for errors. For example if 1% errors, then adjusted 99% is 100%
	var adjustedPercentile float64
	if errors > 0 {
		fmt.Printf("💥  %f%% of requests were errors\n", float64(100*errors)/float64(hist.TotalCount()+errors))
		adjustedPercentile = (float64(hist.TotalCount()+errors) * percentile) / float64(hist.TotalCount())
		if adjustedPercentile > 100 {
			fmt.Printf("💥  Failed at %d req/sec (too many errors)\n", rps)
			return false
		}
	} else {
		adjustedPercentile = percentile
	}

	latency := time.Duration(hist.ValueAtQuantile(adjustedPercentile)) * time.Microsecond
	if latency > sla {
		fmt.Printf("💥  Failed at %d req/sec (latency %s)\n", rps, latency)
		return false
	}
	fmt.Printf("✨  Success at %d req/sec (latency %s)\n", rps, latency)
	return true
}

func main() {
	flag.Usage = usage
	var duration time.Duration
	var percentile float64
	var scaleupPercent float64
	var scaleupSteps int
	var rps int
	var sla time.Duration
	flag.IntVar(&rps, "rps", 20, "Starting requests per second")
	flag.DurationVar(&sla, "sla", 500*time.Millisecond, "Max acceptable latency")
	flag.DurationVar(&duration, "duration", time.Minute, "Duration for each latency test")
	flag.Float64Var(&percentile, "percentile", 99, "The percentile that latency is measured at")
	flag.Float64Var(&scaleupPercent, "scaleup-percent", 0.1, "Percent of duration to scale up rps before each latency test")
	flag.IntVar(&scaleupSteps, "scaleup-steps", 10, "number of steps to go from 0 to max rps")
	flag.Parse()
	if flag.NArg() == 0 || scaleupPercent < 0 {
		flag.Usage()
		os.Exit(1)
	}
	url := flag.Arg(0)
	scaleupDuration := time.Duration(scaleupPercent*duration.Seconds()) * time.Second

	okRate := 1
	var nokRate int
	// first, find the point at which the system breaks
	for {
		if testRate(rps, sla, duration, percentile, scaleupDuration, scaleupSteps, url) {
			okRate = rps
			rps *= 2
		} else {
			nokRate = rps
			break
		}
	}

	// next, do a binary search between okRate and nokRate
	for (nokRate - okRate) > 1 {
		rps = (nokRate + okRate) / 2
		if testRate(rps, sla, duration, percentile, scaleupDuration, scaleupSteps, url) {
			okRate = rps
		} else {
			nokRate = rps
		}
	}
	fmt.Printf("➡️  Maximum Working Rate: %d req/sec\n", okRate)
	os.Rename(fmt.Sprintf("lat_%d.txt", rps), fmt.Sprintf("lat_%d_best.txt", rps))
}
