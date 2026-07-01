package registry

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
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
