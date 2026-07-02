package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestRegistryRegisterTool(t *testing.T) {
	reg := New()
	if err := reg.RegisterTool("echo", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Tool("echo"); !ok {
		t.Fatal("expected registered tool")
	}
	if err := reg.RegisterTool("echo", builtin.NewEchoTool()); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if err := reg.RegisterTool("", builtin.NewEchoTool()); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestRegistryConcurrentRegisterAndLookupIsRaceFree(t *testing.T) {
	reg := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		name := fmt.Sprintf("tool-%d", i)
		go func() {
			defer wg.Done()
			_ = reg.RegisterTool(name, builtin.NewEchoTool())
		}()
		go func() {
			defer wg.Done()
			reg.Tool(name)
		}()
	}
	wg.Wait()
}

func TestRegistryResolveTool(t *testing.T) {
	reg := New()
	echo := builtin.NewEchoTool()
	if err := reg.RegisterTool("echo", echo); err != nil {
		t.Fatal(err)
	}
	executor, ok, err := reg.ResolveTool(context.Background(), core.Tool{Name: "echo"})
	if err != nil || !ok || executor == nil {
		t.Fatalf("ResolveTool echo: ok=%v err=%v executor=%v", ok, err, executor)
	}
	_, ok, err = reg.ResolveTool(context.Background(), core.Tool{Name: "missing"})
	if err != nil || ok {
		t.Fatalf("expected missing tool, ok=%v err=%v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := reg.ResolveTool(ctx, core.Tool{Name: "echo"}); err == nil {
		t.Fatal("expected cancelled context error")
	}
}
