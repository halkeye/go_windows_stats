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
// https://msdn.microsoft.com/en-us/library/aa394515(v=vs.85).aspx
type Win32_Volume struct {
	Capacity   uint64
	DriveType  uint32
	FileSystem string
	FreeSpace  uint64
	Label      *string
	Name       string
}

// Win32_ComputerSystem - wmic ComputerSystem
// https://msdn.microsoft.com/en-us/library/aa394102(v=vs.85).aspx
type Win32_ComputerSystem struct {
	TotalPhysicalMemory uint64
}

// Win32_OperatingSystem - blah blah
// https://msdn.microsoft.com/en-us/library/aa394239(v=vs.85).aspx
type Win32_OperatingSystem struct {
	FreePhysicalMemory     uint64
	FreeVirtualMemory      uint64
	NumberOfProcesses      uint64
	TotalVirtualMemorySize uint64
	TotalVisibleMemorySize uint64
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

func getOperatingSystemStats() (stats []Stat) {
	var dst []Win32_OperatingSystem

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		log.Fatal(err)
	}
	stats = append(stats, Stat{"mem.physical.free", strconv.FormatUint(dst[0].FreePhysicalMemory, 10), time.Now().Unix()})
	stats = append(stats, Stat{"mem.virtual.free", strconv.FormatUint(dst[0].FreeVirtualMemory, 10), time.Now().Unix()})
	stats = append(stats, Stat{"mem.virtual.total", strconv.FormatUint(dst[0].TotalVirtualMemorySize, 10), time.Now().Unix()})
	stats = append(stats, Stat{"mem.visible.total", strconv.FormatUint(dst[0].TotalVisibleMemorySize, 10), time.Now().Unix()})
	stats = append(stats, Stat{"processes.total", strconv.FormatUint(dst[0].NumberOfProcesses, 10), time.Now().Unix()})

	return
}

func getComputerSystemStats() (stats []Stat) {
	var dst []Win32_ComputerSystem

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		log.Fatal(err)
	}
	stats = append(stats, Stat{"mem.physical.total", strconv.FormatUint(dst[0].TotalPhysicalMemory, 10), time.Now().Unix()})
	return
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

		stats = append(stats, Stat{fmt.Sprintf("%s.percent_used", keyPrefix), strconv.FormatInt(percent, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.free", keyPrefix), strconv.FormatUint(v.FreeSpace, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.avail", keyPrefix), strconv.FormatUint(v.Capacity, 10), time.Now().Unix()})
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
	stats = append(stats, getComputerSystemStats()...)
	stats = append(stats, getOperatingSystemStats()...)
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
