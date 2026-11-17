package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Each input is one ordinary file, bounded independently of its reported size.
const maxInputBytes = 1 << 20

type options struct {
	task, member, state, policy, format string
	at                                  time.Time
	missing                             []string
}

func parseOptions(args []string) (options, error) {
	o := options{format: "json"}
	if len(args) == 0 || args[0] != "recommend" {
		return o, errors.New("expected recommend subcommand")
	}
	var at string
	values := map[string]*string{"task": &o.task, "member": &o.member, "state": &o.state, "policy": &o.policy, "format": &o.format, "at": &at}
	seen := map[string]bool{}
	invalid := false
	// Parse every flag before rejecting syntax, so --format is independent of order.
	for i := 1; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			invalid = true
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		dst, known := values[name]
		if !known {
			invalid = true
			continue
		}
		if seen[name] {
			invalid = true
		}
		seen[name] = true
		if !inline {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				invalid = true
				continue
			}
			i++
			value = args[i]
		}
		*dst = value
	}
	if o.format != "json" && o.format != "text" {
		o.format = "json"
		invalid = true
	}
	if invalid {
		return o, errors.New("invalid flags or arguments")
	}
	if o.format != "json" && o.format != "text" {
		o.format = "json"
		return o, errors.New("unsupported format")
	}
	for _, v := range []struct{ name, value string }{{"task", o.task}, {"member", o.member}, {"state", o.state}, {"policy", o.policy}, {"at", at}} {
		if v.value == "" {
			o.missing = append(o.missing, v.name)
		}
	}
	if at != "" {
		var err error
		o.at, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return o, errors.New("invalid decision time")
		}
	}
	return o, nil
}

func readInput(path string) ([]byte, error) {
	// O_NONBLOCK prevents a FIFO from hanging intake; O_NOFOLLOW refuses links.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "input")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not an ordinary file")
	}
	if info.Size() > maxInputBytes {
		return nil, errors.New("input exceeds byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("input exceeds byte limit")
	}
	return data, nil
}
