// minute2win-server: arm machines for a zero-touch wipe, hand iPXE their boot script, and show deployments live.
//
//	minute2win-server -listen :8080 -manifests ../manifests -data ./data
//
// Targets' iPXE (dnsmasq dhcp-boot, or a USB boot drive's autoexec) runs: chain http://<this>:8080/boot/${net0/mac}
// Set M2W_TOKEN to require a token for arming and disarming.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	listen := flag.String("listen", ":8080", "address to serve on")
	manifests := flag.String("manifests", "../manifests", "folder of deployment manifests (*.json)")
	data := flag.String("data", "data", "folder for state.json (\"\" keeps state in memory)")
	base := flag.String("image-base", "https://boot.spillerstech.us/windows11", "URL holding wimboot, BCD, boot.sdi and boot.wim")
	ttl := flag.Duration("arm-ttl", 2*time.Hour, "an arm that no machine boots within this long expires")
	flag.Parse()

	s := &Server{ManifestDir: *manifests, ImageBase: *base, Token: os.Getenv("M2W_TOKEN"), ArmTTL: *ttl, ReportTTL: 3 * time.Hour, Now: time.Now}
	if *data != "" {
		if err := os.MkdirAll(*data, 0o700); err != nil {
			log.Fatal(err)
		}
		s.StatePath = filepath.Join(*data, "state.json")
	}
	if err := s.Load(); err != nil {
		log.Fatal(err)
	}
	if s.Token == "" {
		log.Print("warning: M2W_TOKEN is not set; anyone who can reach this server can arm a machine for a wipe")
	}
	log.Printf("minute2win-server on %s, manifests %s, images %s", *listen, *manifests, *base)
	log.Fatal(http.ListenAndServe(*listen, s.Handler()))
}
