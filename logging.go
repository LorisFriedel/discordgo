// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains code related to discordgo package logging

package discordgo

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

func init() {
	log.AddHook(&SourceCodeHook{})
}

type SourceCodeHook struct {
}

func (sch *SourceCodeHook) Levels() []log.Level {
	return log.AllLevels
}

func (sch *SourceCodeHook) Fire(e *log.Entry) error {
	timestamp := time.Now().Unix()
	file, function, line := findCaller(5)
	e.Message = fmt.Sprintf("[%d] %s:%d:%s() ", timestamp, file, line, function) + e.Message
	return nil
}


// TODO rewrite
// NOT PERF but it works
func findCaller(skip int) (string, string, int) {
	var (
		pc       uintptr
		file     string
		function string
		line     int
	)

	for i := 0; i < 10; i++ {
		pc, file, line = getCaller(skip + i)
		if !strings.HasPrefix(file, "logrus") {
			break
		}
	}

	if pc != 0 {
		frames := runtime.CallersFrames([]uintptr{pc})
		frame, _ := frames.Next()
		function = frame.Function
	}

	return file, function, line
}

func getCaller(skip int) (uintptr, string, int) {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return 0, "", 0
	}

	n := 0
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' {
			n += 1
			if n >= 2 {
				file = file[i+1:]
				break
			}
		}
	}

	return pc, file, line
}
