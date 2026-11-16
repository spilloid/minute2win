package main

import (
	"errors"
	"time"
)

type armReceipt struct {
	RunID    string    `json:"run_id"`
	MAC      string    `json:"mac"`
	Manifest string    `json:"manifest"`
	ArmedAt  time.Time `json:"armed_at"`
	Expires  time.Time `json:"expires"`
}

var errRequestConflict = errors.New("request_id was already used with a different MAC or image; it was not executed again")

func (r *armReceipt) replay(mac, manifest string) (*Armed, bool, error) {
	if mac != r.MAC || manifest != r.Manifest {
		return nil, false, errRequestConflict
	}
	return &Armed{RunID: r.RunID, MAC: r.MAC, Manifest: r.Manifest, ArmedAt: r.ArmedAt, Expires: r.Expires}, true, nil
}
