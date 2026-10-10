package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture swaps a limiter's output for a recorder.
func capture(l *logLimiter) (lines func() []string) {
	var mu sync.Mutex
	var got []string
	l.mu.Lock()
	l.out = func(f string, a ...any) { mu.Lock(); got = append(got, fmt.Sprintf(f, a...)); mu.Unlock() }
	l.mu.Unlock()
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

// floodNoise exhausts the noise class the way an attacker can: many distinct, unauthenticated, made-up keys.
func floodNoise(l *logLimiter) {
	for i := 0; i < 5000; i++ {
		l.noisef(fmt.Sprintf("flood:%d", i), "noise %d", i)
	}
}

// Review round 3 (C): a revoked arm at the final recheck is a PROTECTED diagnostic. Unauthenticated noise must not be able to hide it,
// and it must not share a suppression key with the ordinary "not armed" line for the same MAC.
func TestRecheckRejectionSurvivesANoiseFlood(t *testing.T) {
	f := newFixture(t, "")
	client, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer client.Close()
	p := startLoopbackPXE(t, f, t.TempDir(), client)
	lines := capture(p.lim)
	f.arm(t, armedMAC)
	hook := func() { f.s.doDisarm(armedMAC) }
	p.beforeSend.Store(&hook)
	floodNoise(p.lim)
	// Burn the ordinary "not armed" key for this MAC too, so a shared key would suppress the recheck line.
	p.lim.noisef("notarmed:"+armedMAC, "pxe: %s not armed, ignoring", armedMAC)
	client.WriteToUDP(pxeDiscover(armedMAC, 7).bytes(), p.DHCPAddr())
	if r := recvDHCP(t, client, 400*time.Millisecond); r != nil {
		t.Fatal("replied after the arm was revoked")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, l := range lines() {
			if strings.Contains(l, "no longer armed") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the revoked-arm decision was not logged while noise was flooding; lines: %q", lines())
}

// Review round 3 (C): TFTP listener read failures are protected diagnostics too, and a persistent failure must not busy-loop.
func TestTFTPReadFailureIsProtectedAndPaced(t *testing.T) {
	ts := newTestTFTP(t, t.TempDir())
	lines := capture(ts.lim)
	floodNoise(ts.lim)
	ts.readFailed(errors.New("read udp: boom"))
	found := false
	for _, l := range lines() {
		if strings.Contains(l, "tftp: read:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a listener read failure was dropped by the noise budget: %q", lines())
	}
	if readErrBackoff < 10*time.Millisecond {
		t.Fatalf("read-error backoff %v is too small to stop a busy loop", readErrBackoff)
	}
}
