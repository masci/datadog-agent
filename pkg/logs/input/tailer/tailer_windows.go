// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2018 Datadog, Inc.

// +build windows

package tailer

import (
	"io"
	"os"
	"path/filepath"

	log "github.com/cihub/seelog"

	"github.com/DataDog/datadog-agent/pkg/logs/decoder"
)

func (t *Tailer) startReading(offset int64, whence int) error {
	var err error
	t.fullpath, err = filepath.Abs(t.path)
	if err != nil {
		t.source.Tracker.TrackError(err)
		return err
	}
	log.Info("startReading ", t.fullpath)
	/*
		f, err := os.Open(fullpath)
		if err != nil {
			t.source.Tracker.TrackError(err)
			return err
		}
		t.source.Tracker.TrackSuccess()

		ret, _ := f.Seek(offset, whence)
		t.file = f
	*/
	t.readOffset = offset
	t.decodedOffset = offset

	go t.readForever()
	return nil
}

func (t *Tailer) readAvailable() (err error) {
	err = nil
	f, err := os.Open(t.fullpath)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err == nil {
		sz := st.Size()
		log.Debugf("Size is %d, offset is %d", sz, t.GetReadOffset())
		if sz == 0 {
			log.Debug("File size now zero, resetting offset")
			t.SetReadOffset(0)
		} else if sz < t.GetReadOffset() {
			log.Debug("Offset off end of file, resetting")
			t.SetReadOffset(0)
		}
	} else {
		log.Debugf("Error stat()ing file %v", err)
	}
	f.Seek(t.GetReadOffset(), io.SeekStart)
	inBuf := make([]byte, 4096)

	for {
		n, err := f.Read(inBuf)
		if n == 0 || err != nil {
			log.Debugf("Done reading")
			break
		}
		log.Debugf("Sending %d bytes to input channel", n)
		t.d.InputChan <- decoder.NewInput(inBuf[:n])
		t.incrementReadOffset(n)
	}
	return nil

}

// readForever lets the tailer tail the content of a file
// until it is closed.
func (t *Tailer) readForever() {

	for {
		if t.shouldHardStop() {
			t.onStop()
			return
		}
		t.wait()
		err := t.readAvailable()

		if err == io.EOF || os.IsNotExist(err) {
			if t.shouldSoftStop() {
				t.onStop()
				return
			}
			continue
		}
		if err != nil {
			t.source.Tracker.TrackError(err)
			log.Error("Err: ", err)
			return
		}
	}
}
func (t *Tailer) checkForRotation() (bool, error) {
	return false, nil
}
