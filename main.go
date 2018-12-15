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
	metrics := vegeta.Metrics{}

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
	for res := range attacker.Attack(targeter, vrate, duration, "Latency Test") {
		metrics.Add(res)
		hist.RecordValue(res.Latency.Nanoseconds() / 1e3)
	}
	metrics.Close()

	file, err := os.OpenFile(fmt.Sprintf("lat_%d.txt", rps), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	histwriter.WriteDistribution(hist, histwriter.Logarithmic, 1e-3, file)

	latency := time.Duration(hist.ValueAtQuantile(percentile)) * time.Microsecond
	if 100*metrics.Success < percentile {
		fmt.Printf("💥  Failed at %d req/sec (errors: %f%%)\n", rps, 100*(1-metrics.Success))
		return false
	}
	if metrics.Rate < float64(rps) && float64(rps)-metrics.Rate > 1 {
		fmt.Printf("💥  Failed at %d req/sec (Only managed to get to %f req/sec)\n", rps, metrics.Rate)
		return false
	}
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
	flag.Float64Var(&scaleupPercent, "scaleup-percent", 10, "Percent of duration to scale up rps before each latency test")
	flag.IntVar(&scaleupSteps, "scaleup-steps", 10, "number of steps to go from 0 to max rps")
	flag.Parse()
	if flag.NArg() == 0 || rps <= 0 || scaleupPercent < 0 || scaleupPercent > 100 || scaleupSteps <= 0 || percentile < 0 || percentile > 100 {
		flag.Usage()
		os.Exit(1)
	}
	url := flag.Arg(0)
	scaleupDuration := time.Duration(scaleupPercent/100*duration.Seconds()) * time.Second

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
