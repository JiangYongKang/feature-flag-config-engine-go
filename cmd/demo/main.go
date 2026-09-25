// Command demo runs the engine against local testdata files and prints
// explainable flag decisions, layered property values and a hot-reload
// cycle. Run from the repository root:
//
//	go run ./cmd/demo
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/engine"
	"github.com/highcumontoa/feature-flag-config-engine-go/internal/source"
)

func main() {
	// Priority order: earlier = higher. Override layer wins over base.
	sources := []source.Source{
		source.NewFile("local-override", "testdata/override.json"),
		source.NewFile("base", "testdata/base.json"),
	}

	eng, err := engine.New(sources)
	if err != nil {
		log.Fatalf("initial load rejected: %v", err)
	}
	fmt.Println("active snapshot:", eng.SnapshotVersion())

	cases := []struct {
		name  string
		attrs domain.Attributes
	}{
		{"staff user", domain.Attributes{"user_id": "u-001", "region": "cn", "group": "staff"}},
		{"regular cn user A", domain.Attributes{"user_id": "u-002", "region": "cn"}},
		{"regular cn user B", domain.Attributes{"user_id": "u-003", "region": "cn"}},
		{"eu user", domain.Attributes{"user_id": "u-004", "region": "eu"}},
	}
	for _, c := range cases {
		d, err := eng.EvaluateFlag("checkout_new", c.attrs, nil)
		if err != nil {
			log.Fatalf("evaluate %s: %v", c.name, err)
		}
		fmt.Printf("case=%-20s => variation=%s value=%t reason=%s source=%s\n",
			c.name, d.Variation, d.Value, d.Reason, d.Source)
	}

	// Layered property: "theme" exists in both layers; override wins.
	if theme, src, err := eng.GetString("theme"); err != nil {
		log.Fatalf("get theme: %v", err)
	} else {
		fmt.Printf("property theme=%q source=%s\n", theme, src)
	}

	// Demonstrates that hot reload re-reads files; failure keeps old config.
	if err := eng.Reload(); err != nil {
		log.Fatalf("reload: %v", err)
	}
	fmt.Println("after reload snapshot:", eng.SnapshotVersion())

	if err := demoBadLayer(); err != nil {
		fmt.Println("invalid layer correctly rejected:", err)
		os.Exit(0)
	}
}

// demoBadLayer shows an invalid source is rejected with a precise reason
// instead of silently downgrading to defaults.
func demoBadLayer() error {
	bad := []source.Source{
		source.NewFile("bad", "testdata/bad_type.json"),
		source.NewFile("base", "testdata/base.json"),
	}
	_, err := engine.New(bad)
	return err
}
