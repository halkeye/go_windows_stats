// +build windows

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	LastBootUpTime         time.Time
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
	stats = append(stats, Stat{
		"uptime",
		strconv.FormatUint(uint64(time.Since(dst[0].LastBootUpTime).Seconds()), 10),
		time.Now().Unix(),
	})
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

var nonAlphaNumReg = regexp.MustCompile("[^A-Za-z0-9]+")

func happyDriveName(driveLetter string) string {
	return nonAlphaNumReg.ReplaceAllLiteralString(driveLetter, "")
}

func getDiskStats() (stats []Stat) {
	var dst []Win32_Volume

	q := wmi.CreateQuery(&dst, "Where DriveType=3 and NOT Label='System Reserved'")
	err := wmi.Query(q, &dst)
	if err != nil {
		log.Fatal(err)
	}
	for _, v := range dst {
		keyPrefix := fmt.Sprintf("disk.%s", happyDriveName(v.Name))
		percent := int64(100 * (float64(v.FreeSpace) / float64(v.Capacity)))

		stats = append(stats, Stat{fmt.Sprintf("%s.percent_used", keyPrefix), strconv.FormatInt(percent, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.free", keyPrefix), strconv.FormatUint(v.FreeSpace, 10), time.Now().Unix()})
		stats = append(stats, Stat{fmt.Sprintf("%s.avail", keyPrefix), strconv.FormatUint(v.Capacity, 10), time.Now().Unix()})
	}

	return
}

func callTypePerf(fields []string) (headers []string, records [][]string) {
	var (
		cmdOut []byte
		err    error
	)
	cmdName := "typeperf"
	cmdArgs := []string{
		"-sc",
		"1",
	}
	cmdArgs = append(cmdArgs, fields...)
	if cmdOut, err = exec.Command(cmdName, cmdArgs...).Output(); err != nil {
		fmt.Fprintln(os.Stderr, "There was an error running typeperf command: ", err)
		os.Exit(1)
	}
	var lines []string
	for _, line := range strings.Split(string(cmdOut), "\n") {
		if strings.HasPrefix(line, "\"") {
			lines = append(lines, line)
		}
	}

	r := csv.NewReader(strings.NewReader(strings.Join(lines, "\n")))
	records, err = r.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	headers, records = records[0], records[1:]
	return
}

var logicalDiskRegex = regexp.MustCompile("^LogicalDisk\\(([^)]+)\\)$")
var logicalDiskStatMap = map[string]string{
	"Avg. Disk sec/Read":  "disk_read_io",
	"Avg. Disk sec/Write": "disk_write_io",
}

func getCPUStats() (stats []Stat) {
	headers, records := callTypePerf([]string{
		"processor(_total)\\% processor time",
		"LogicalDisk(*)\\Avg. Disk sec/Read",
		"LogicalDisk(*)\\Avg. Disk sec/Write",
	})
	for headerNum, header := range headers[1:] {
		headerParts := strings.Split(header, "\\")
		headers[headerNum] = strings.Join(headerParts[3:len(headerParts)], "\\")
	}
	for _, row := range records {
		for colNum, col := range row[1:] {
			headerParts := strings.Split(headers[colNum], "\\")

			if strings.HasPrefix(headerParts[0], "processor(") {
				stats = append(stats, Stat{"cpu", col, time.Now().Unix()})
				continue
			}

			if results := logicalDiskRegex.FindStringSubmatch(headerParts[0]); results != nil {
				if results[1] == "_Total" {
					continue
				}
				if strings.HasPrefix(results[1], "HarddiskVolume") {
					continue
				}

				keyPrefix := fmt.Sprintf("disk.%s", happyDriveName(results[1]))
				fieldName, ok := logicalDiskStatMap[strings.Join(headerParts[1:], "/")]
				if ok {
					stats = append(stats, Stat{
						fmt.Sprintf("%s.%s", keyPrefix, fieldName),
						col,
						time.Now().Unix(),
					})
					continue
				}
			}

			// happyDriveName
			log.Printf("%s = %s", headers[colNum], col)
		}
	}
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
			stat, stats = stats[0], stats[1:]
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
