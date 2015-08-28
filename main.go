// +build windows

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Stat represents stats to send to graphite
type Stat struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	DT    int64  `json:"dt"`
}

func schedule(what func(), delay time.Duration) chan bool {
	stop := make(chan bool)

	go func() {
		for {
			what()
			select {
			case <-time.After(delay):
			case <-stop:
				return
			}
		}
	}()

	return stop
}

func getCPUStats() (stat Stat) {
	var out bytes.Buffer
	cmd := exec.Command("typeperf", "-sc", "1", "processor(_total)\\% processor time")
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}

	chunks := strings.Split(out.String(), ",")
	chunks = strings.Split(chunks[2], "\"")
	stat = Stat{"cpu", chunks[1], time.Now().Unix()}
	return
}

func getStats() (stats []Stat) {
	stats = append(stats, getCPUStats())
	return
}

func main() {
	var stats []Stat
	getStatsInterval := func() {
		newStats := getStats()
		stats = append(stats, newStats...)
	}
	outputStatsInterval := func() {
		var stat Stat
		for len(stats) != 0 {
			stat, stats = stats[len(stats)-1], stats[:len(stats)-1]
			b, _ := json.Marshal(stat)
			log.Printf("Output: %s", string(b))
		}
	}
	/*stopGet := */ schedule(getStatsInterval, 5*time.Second)
	/*stopOutput := */ schedule(outputStatsInterval, 5*time.Millisecond)
	for {
		time.Sleep(100 * time.Second)
	}
}
