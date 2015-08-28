// +build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/StackExchange/wmi"
)

// Win32_Volume - wmic volume where DriveType=3 list brief
type Win32_Volume struct {
	Capacity   int64
	DriveType  int64
	FileSystem string
	FreeSpace  int64
	Label      *string
	Name       string
}

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

func getDiskStats() (stats []Stat) {
	var dst []Win32_Volume
	nonAlphaNumReg, _ := regexp.Compile("[^A-Za-z0-9]+")

	q := wmi.CreateQuery(&dst, "Where DriveType=3 and NOT Label='System Reserved'")
	err := wmi.Query(q, &dst)
	if err != nil {
		log.Fatal(err)
	}
	for _, v := range dst {
		keyPrefix := fmt.Sprintf("disk.%s", nonAlphaNumReg.ReplaceAllLiteralString(v.Name, "_"))
		percent := int64(100 * (float64(v.FreeSpace) / float64(v.Capacity)))

		b, _ := json.Marshal(v)
		log.Printf("Disk: %s", string(b))

		stats = append(stats, Stat{fmt.Sprintf("%s.percent_used", keyPrefix), strconv.FormatInt(percent, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.free", keyPrefix), strconv.FormatInt(v.FreeSpace, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.avail", keyPrefix), strconv.FormatInt(v.Capacity, 10), time.Now().Unix()})
	}

	return
}
func getCPUStats() (stats []Stat) {
	var out bytes.Buffer
	cmd := exec.Command("typeperf", "-sc", "1", "processor(_total)\\% processor time")
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}

	chunks := strings.Split(out.String(), ",")
	chunks = strings.Split(chunks[2], "\"")
	stats = append(stats, Stat{"cpu", chunks[1], time.Now().Unix()})
	return
}

func getStats() (stats []Stat) {
	stats = append(stats, getCPUStats()...)
	stats = append(stats, getDiskStats()...)
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
