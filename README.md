go_windows_stats
================

Simple little script to pull stats for a windows machine.

Reason for Existence
====================

I found many scripts out there that would pull stats. Most of them used powershell or were paid.
I couldn't get the powershell scripts past our security setup and didn't have the budget for paid setups.

Usage
=====
```
Usage of go_windows_stats.exe:
  -computerName string
        Computer Name (default "<hostname>")
  -graphite
        Enable Graphite
  -graphiteHost string
        graphite hostname (default "localhost")
  -graphitePort int
        graphite port (default 2003)
```

I mostly run it as `go_windows_stats.exe -graphite -graphiteHost odin` and thats good enough for me
