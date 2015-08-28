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
	stats = append(stats, getCPUStats(), getCPUStats(), getCPUStats())
	return
}
func main() {
	b, _ := json.Marshal(getStats())
	log.Printf("Output: %s", string(b))
}
