package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/codahale/hdrhistogram"
	ct "github.com/daviddengcn/go-colortext"
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

	//Limit scaleup steps to numder of seconds for scaling up
	//Vegeta doesn't seem to like shorter attacks
	if int(scaleupDuration.Seconds()) < scaleupSteps {
		scaleupSteps = int(scaleupDuration.Seconds())

	}
	scaleupRate := float64(rps) / float64(scaleupSteps)
	subDuration := scaleupDuration / time.Duration(scaleupSteps)
	fmt.Printf("%s Starting %d req/sec Scaleup for %s...\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, scaleupDuration)
	for i := 0; i < scaleupSteps; i++ {
		r := int(math.Ceil(float64(i+1) * scaleupRate))
		if r > rps {
			r = rps
		}
		vrate := vegeta.Rate{Freq: r, Per: time.Second}
		for range attacker.Attack(targeter, vrate, subDuration, "Scale Up") {
		}
	}
	fmt.Printf("%s Starting %d req/sec Load Test for %s...\n", time.Now().Format("[2006-01-02T15:04:05]"), rps, duration)
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
	var percentile float64
	var scaleupPercent float64
	var rpsAccuracy float64
	var scaleupSteps int
	var rps int
	var sla time.Duration
	flag.IntVar(&rps, "rps", 20, "Starting requests per second")
	flag.DurationVar(&sla, "sla", 500*time.Millisecond, "Max acceptable latency")
	flag.DurationVar(&duration, "duration", time.Minute, "Duration for each latency test")
	flag.Float64Var(&percentile, "percentile", 99.9, "The percentile that latency is measured at")
	flag.Float64Var(&scaleupPercent, "scaleup-percent", 10, "Percent of duration to scale up rps before each latency test")
	flag.Float64Var(&rpsAccuracy, "rps-accuracy", 100, "How close the output should be to the correct rps. 100 is exact rps. 95 would be within 5%")
	flag.IntVar(&scaleupSteps, "scaleup-steps", 10, "number of steps to go from 0 to max rps")
	flag.Parse()
	if flag.NArg() == 0 || rps <= 0 || scaleupPercent < 0 || scaleupPercent > 100 || scaleupSteps <= 0 {
		flag.Usage()
		os.Exit(1)
	}
	if percentile < 0 || percentile > 100 || rpsAccuracy > 100 || rpsAccuracy <= 0 {
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
	rpsAccuracy = rpsAccuracy / 100.0
	for float64(okRate)/float64(nokRate-1) < rpsAccuracy {
		rps = (nokRate + okRate) / 2
		if testRate(rps, sla, duration, percentile, scaleupDuration, scaleupSteps, url) {
			okRate = rps
		} else {
			nokRate = rps
		}
	}
	if nokRate-1 == okRate {
		fmt.Printf("Maximum Working Rate: %d req/sec\n", okRate)
	} else {
		fmt.Printf("Maximum Working Rate Within: %d-%d req/sec\n", okRate, (nokRate - 1))
	}
	os.Rename(fmt.Sprintf("lat_%d.txt", okRate), fmt.Sprintf("lat_%d_best.txt", okRate))
}
