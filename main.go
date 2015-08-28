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

func main() {
	var stats []Stat
	var out bytes.Buffer

	cmd := exec.Command("typeperf", "-sc", "1", "processor(_total)\\% processor time")
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}

	chunks := strings.Split(out.String(), ",")
	chunks = strings.Split(chunks[2], "\"")
	stat := Stat{"cpu", chunks[1], time.Now().Unix()}
	stats = append(stats, stat)

	b, err := json.Marshal(stat)
	log.Printf("Output: %s", string(b))
}
