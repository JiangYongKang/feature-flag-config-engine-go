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

	// 发布前试算：一份灰度 25% 的候选内容，只出结论不动线上
	candidate := &engine.ContentSource{
		SrcName:     "candidate",
		SrcPriority: 0,
		Content: `{"flags":[
			{"key":"new-checkout","type":"bool","default":false,
			 "rollout":{"attribute":"user_id","percentage":25}},
			{"key":"banner-text","type":"string","default":"hello"}
		]}`,
	}
	rep, err := eng.Simulate(engine.SimulationOptions{
		Probes: map[string][]string{"user_id": {"alice", "bob", "carol"}},
	}, candidate)
	if err != nil {
		log.Fatalf("simulate: %v", err)
	}
	fmt.Printf("simulate: added=%v deleted=%v modified=%v unchanged=%v (线上仍为 v%d)\n",
		rep.AddedKeys, rep.DeletedKeys, rep.ModifiedKeys, rep.UnchangedKeys,
		eng.Snapshot().Version)
	for _, ch := range rep.Changes {
		if ch.Rollout != nil {
			fmt.Printf("  rollout trend key=%s direction=%s %.0f%%->%.0f%%\n",
				ch.Key, ch.Rollout.Direction,
				ch.Rollout.BeforePercentage, ch.Rollout.AfterPercentage)
			for _, p := range ch.Rollout.Probes {
				fmt.Printf("    probe %s=%s before=%v after=%v %s\n",
					p.AttrName, p.AttrValue, p.BeforeHit, p.AfterHit, p.Note)
			}
		}
	}

	// 正式发布候选并验证灰度变化，然后整体回滚到历史 v1
	if err := eng.Publish(candidate); err != nil {
		log.Fatalf("publish: %v", err)
	}
	d, _ = eng.Evaluate("new-checkout", map[string]string{"user_id": "alice"})
	published := d.Value.Bool
	fmt.Printf("published candidate: alice new-checkout=%v version=%d\n",
		d.Value.Bool, d.Version)
	rb, err := eng.Rollback(1)
	if err != nil {
		log.Fatalf("rollback: %v", err)
	}
	d, _ = eng.Evaluate("new-checkout", map[string]string{"user_id": "alice"})
	fmt.Printf("rollback to v1 content (now v%d): alice new-checkout=%v (回滚前发布版=%v)\n",
		rb.Version, d.Value.Bool, published)
}
