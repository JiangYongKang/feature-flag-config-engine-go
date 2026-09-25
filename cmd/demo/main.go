// demo 演示：文件来源 + 内存覆盖来源合并、热更新与按属性灰度求值。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/highcumontoa/feature-flag-config-engine-go/engine"
)

func main() {
	dir, err := os.MkdirTemp("", "ffdemo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "flags.json")

	base := `{"flags":[
		{"key":"new-checkout","type":"bool","default":false,
		 "rollout":{"attribute":"user_id","percentage":50}},
		{"key":"banner-text","type":"string","default":"hello"}
	]}`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		log.Fatal(err)
	}

	fileSrc := &engine.FileSource{SrcName: "file", SrcPriority: 0, Path: path}
	override := engine.NewMapSource("override", 10, map[string]engine.FlagDef{
		"banner-text": {
			Key: "banner-text", Type: engine.TypeString,
			Default: engine.Value{Type: engine.TypeString, Str: "hello from override"},
		},
	})

	eng, err := engine.New(fileSrc, override)
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}

	for _, uid := range []string{"alice", "bob", "carol"} {
		d, err := eng.Evaluate("new-checkout", map[string]string{"user_id": uid})
		if err != nil {
			log.Fatalf("eval: %v", err)
		}
		fmt.Printf("user=%-6s new-checkout=%-5v reason=%s bucket=%d version=%d\n",
			uid, d.Value.Bool, d.Reason, d.Bucket, d.Version)
	}
	d, _ := eng.Evaluate("banner-text", nil)
	fmt.Printf("banner-text=%q reason=%s (高优先级 override 来源生效)\n", d.Value.Str, d.Reason)

	// 热更新：把灰度比例调到 100%
	updated := `{"flags":[
		{"key":"new-checkout","type":"bool","default":false,
		 "rollout":{"attribute":"user_id","percentage":100}},
		{"key":"banner-text","type":"string","default":"hello"}
	]}`
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := eng.Reload(); err != nil {
		log.Fatalf("reload: %v", err)
	}
	d, _ = eng.Evaluate("new-checkout", map[string]string{"user_id": "alice"})
	fmt.Printf("after hot update: alice new-checkout=%v reason=%s version=%d\n",
		d.Value.Bool, d.Reason, d.Version)

	// 失败的热更新：写入非法配置，旧快照继续生效
	if err := os.WriteFile(path, []byte(`{"flags":[{"key":"x","type":"bool","default":"oops"}]}`), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := eng.Reload(); err != nil {
		fmt.Printf("bad reload rejected: %v\n", err)
	}
	d, _ = eng.Evaluate("new-checkout", map[string]string{"user_id": "alice"})
	fmt.Printf("after failed reload: alice new-checkout=%v version=%d (旧配置继续生效)\n",
		d.Value.Bool, d.Version)
}
