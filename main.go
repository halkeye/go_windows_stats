// +build windows

package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/StackExchange/wmi"
	"github.com/marpaia/graphite-golang"
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
	Key   string    `json:"key"`
	Value string    `json:"value"`
	DT    time.Time `json:"dt"`
}

// Config - structure for various config attributes
type Config struct {
	computerName    string
	graphiteHost    string
	graphitePort    int
	graphiteMode    string
	graphiteEnabled bool
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
		log.Fatalf("getOperatingSystemStats: %s", err)
	}
	stats = append(stats, Stat{"mem.physical.free", strconv.FormatUint(dst[0].FreePhysicalMemory, 10), time.Now().UTC()})
	stats = append(stats, Stat{"mem.virtual.free", strconv.FormatUint(dst[0].FreeVirtualMemory, 10), time.Now().UTC()})
	stats = append(stats, Stat{"mem.virtual.total", strconv.FormatUint(dst[0].TotalVirtualMemorySize, 10), time.Now().UTC()})
	stats = append(stats, Stat{"mem.visible.total", strconv.FormatUint(dst[0].TotalVisibleMemorySize, 10), time.Now().UTC()})
	stats = append(stats, Stat{"processes.total", strconv.FormatUint(dst[0].NumberOfProcesses, 10), time.Now().UTC()})
	stats = append(stats, Stat{
		"uptime",
		strconv.FormatUint(uint64(time.Since(dst[0].LastBootUpTime).Seconds()), 10),
		time.Now().UTC(),
	})
	return
}

func getComputerSystemStats() (stats []Stat) {
	var dst []Win32_ComputerSystem

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		log.Fatalf("getComputerSystemStats: %s", err)
	}
	stats = append(stats, Stat{"mem.physical.total", strconv.FormatUint(dst[0].TotalPhysicalMemory, 10), time.Now().UTC()})
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
		log.Fatalf("getDiskStats: %s", err)
	}
	for _, v := range dst {
		keyPrefix := fmt.Sprintf("disk.%s", happyDriveName(v.Name))
		percent := int64(100 * (float64(v.FreeSpace) / float64(v.Capacity)))

		stats = append(stats, Stat{fmt.Sprintf("%s.percent_used", keyPrefix), strconv.FormatInt(percent, 10), time.Now().UTC()})
		stats = append(stats, Stat{fmt.Sprintf("%s.free", keyPrefix), strconv.FormatUint(v.FreeSpace, 10), time.Now().UTC()})
		stats = append(stats, Stat{fmt.Sprintf("%s.avail", keyPrefix), strconv.FormatUint(v.Capacity, 10), time.Now().UTC()})
	}

	return
}

type typeperfPair struct {
	regexp       *regexp.Regexp
	template     string
	allowedTotal bool
}

var typeperfRewrites = []typeperfPair{
	typeperfPair{
		regexp:   regexp.MustCompile("^LogicalDisk\\((?P<driveName>[^)]+)\\)\\\\Avg. Disk sec\\/(?P<readWrite>(?:Read|Write))$"),
		template: "disk.${driveName}.disk_${readWrite}_io",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Processor\\(_Total\\)\\\\% Processor Time$"),
		template: "system.cpu",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^System\\\\Threads$"),
		template: "system.threads",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^System\\\\Processor Queue Length$"),
		template: "system.processor_queue_length",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^PhysicalDisk\\([0-9 ]*(?P<driveName>[^)]+)\\)\\\\Avg. Disk (?P<readWrite>(?:Read|Write)) Queue Length$"),
		template: "disk.${driveName}.disk_${readWrite}_queue_length",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Memory\\\\Pages/sec$"),
		template: "mem.pages",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Memory\\\\Pages Input/sec$"),
		template: "mem.pages",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Network Interface\\((?P<ifName>[^)]+)\\)\\\\Bytes (?P<receivedSent>(?:Received|Sent))/sec$"),
		template: "network.${ifName}.bytes.${receivedSent}",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Network Interface\\((?P<ifName>[^)]+)\\)\\\\Packets (?P<receivedSent>(?:Received|Sent)) Unicast/sec$"),
		template: "network.${ifName}.packets.unicast_${receivedSent}",
	},
	typeperfPair{
		regexp:   regexp.MustCompile("^Network Interface\\((?P<ifName>[^)]+)\\)\\\\Packets (?P<receivedSent>(?:Received|Sent)) Non-Unicast/sec$"),
		template: "network.${ifName}.packets.${receivedSent}",
	},
}

func callTypePerf(fields []string) (stats []Stat) {
	var (
		cmdOut  []byte
		err     error
		headers []string
		records [][]string
	)
	cmdName := "typeperf"
	cmdArgs := []string{
		"-sc",
		"1",
	}
	cmdArgs = append(cmdArgs, fields...)
	if cmdOut, err = exec.Command(cmdName, cmdArgs...).Output(); err != nil {
		log.Fatalf("There was an error running typeperf command [%s %s]: %s", cmdName, cmdArgs, err)
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
		log.Printf("CmdArgs: [%s], Err: %s, String: %s", cmdArgs, err, string(cmdOut))
		return
	}

	headers, records = records[0], records[1:]
	for headerNum, header := range headers[1:] {
		headerParts := strings.Split(header, "\\")
		headers[headerNum] = strings.Join(headerParts[3:len(headerParts)], "\\")
	}
	for _, row := range records {
	ResultLoop:
		for colNum, col := range row[1:] {
			for _, matchers := range typeperfRewrites {
				if results := matchers.regexp.FindStringSubmatch(headers[colNum]); results != nil {
					var variableMap = make(map[string]string)

					if matchers.regexp.NumSubexp() != 0 {
						for nameNo, name := range matchers.regexp.SubexpNames()[1:] {
							if name == "readWrite" || name == "receivedSent" {
								results[nameNo+1] = strings.ToLower(results[nameNo+1])
							}
							if name == "ifName" {
								results[nameNo+1] = happyDriveName(results[nameNo+1])
							}
							if name == "driveName" {
								/* if there are named matches, then don't allow _Total */
								if results[nameNo+1] == "_Total" {
									continue ResultLoop
								}

								/* If its a hidden drive name, ignore - FIXME */
								if strings.HasPrefix(results[nameNo+1], "HarddiskVolume") {
									continue ResultLoop
								}
								results[nameNo+1] = happyDriveName(results[nameNo+1])
							}
							variableMap[name] = results[nameNo+1]
						}
					}

					keyName := os.Expand(matchers.template, func(s string) string {
						return variableMap[s]
					})
					stats = append(stats, Stat{
						keyName,
						col,
						time.Now().UTC(),
					})
					continue ResultLoop
				}
			}
			log.Printf("NOT FOUND --- %s = %s", headers[colNum], col)

		}
	}
	return
}

func getProcessorStats() (stats []Stat) {
	stats = callTypePerf([]string{
		"Processor(_Total)\\% Processor Time",
	})
	return
}
func getTypePerfStats() (stats []Stat) {
	stats = callTypePerf([]string{
		"LogicalDisk(*)\\Avg. Disk sec/Read",
		"LogicalDisk(*)\\Avg. Disk sec/Write",
		"Network Interface(*)\\Bytes Received/sec",
		"Network Interface(*)\\Bytes Sent/sec",
		"Network Interface(*)\\Packets Received Unicast/sec",
		"Network Interface(*)\\Packets Sent Unicast/sec",
		"Network Interface(*)\\Packets Received Non-Unicast/sec",
		"Network Interface(*)\\Packets Sent Non-Unicast/sec",
		//"Memory\\Available MBytes",
		"Memory\\Pages/sec",
		"Memory\\Pages Input/sec",
		"System\\Processor Queue Length",
		"System\\Threads",
		"PhysicalDisk(*)\\Avg. Disk Write Queue Length",
		"PhysicalDisk(*)\\Avg. Disk Read Queue Length",
	})
	return
}

func getStats() (stats []Stat) {
	stats = append(stats, getTypePerfStats()...)
	stats = append(stats, getDiskStats()...)
	stats = append(stats, getProcessorStats()...)
	stats = append(stats, getComputerSystemStats()...)
	stats = append(stats, getOperatingSystemStats()...)
	return
}

var config = Config{}

func getGraphite(config Config) (g *graphite.Graphite) {
	var err error

	if config.graphiteEnabled {
		g, err = graphite.NewGraphite(config.graphiteHost, config.graphitePort)
		if err != nil {
			log.Fatal("Error connecting to graphite", err)
		}
	} else {
		g = graphite.NewGraphiteNop(config.graphiteHost, config.graphitePort)
	}
	return
}

func main() {
	var stats []Stat
	var g *graphite.Graphite
	var err error
	hostname, _ := os.Hostname()

	flag.StringVar(&config.computerName, "computerName", hostname, "Computer Name")
	flag.BoolVar(&config.graphiteEnabled, "graphite", false, "Enable Graphite")
	flag.StringVar(&config.graphiteHost, "graphiteHost", "localhost", "graphite hostname")
	flag.IntVar(&config.graphitePort, "graphitePort", 2003, "graphite port")
	//flag.StringVar(&config.graphiteMode, "graphiteMode", "tcp", "tcp or udp")
	flag.Parse()

	g = getGraphite(config)

	getStatsInterval := func() {
		newStats := getStats()
		stats = append(stats, newStats...)
	}
	outputStatsInterval := func() {
		var stat Stat
		for len(stats) != 0 {
			stat, stats = stats[0], stats[1:]
			err = g.SendMetric(graphite.NewMetric(
				fmt.Sprintf("%s.%s", config.computerName, stat.Key),
				stat.Value,
				stat.DT.Unix(),
			))
			if err != nil {
				stats = append(stats, stat)
				log.Printf("Error writing to: %s", err)
				g = getGraphite(config)
			}
		}
	}
	/*stopGet := */ schedule(getStatsInterval, 5*time.Second)
	/*stopOutput := */ schedule(outputStatsInterval, 5*time.Millisecond)
	for {
		time.Sleep(100 * time.Second)
	}
}
