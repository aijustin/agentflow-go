package catalog

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/aijustin/agentflow-go/pkg/core"
)

const (
	APIVersion = "agentflow.dev/v1"
	KindTool   = "Tool"
	KindSkill  = "Skill"
)

type Metadata struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version,omitempty"`
}

type ToolDocument struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       toolSpec `yaml:"spec"`
}

type SkillDocument struct {
	APIVersion string    `yaml:"apiVersion"`
	Kind       string    `yaml:"kind"`
	Metadata   Metadata  `yaml:"metadata"`
	Spec       skillSpec `yaml:"spec"`
}

func LoadToolManifestFile(path string) (core.Tool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Tool{}, err
	}
	return LoadToolManifest(data)
}

func LoadToolManifest(data []byte) (core.Tool, error) {
	var doc ToolDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return core.Tool{}, fmt.Errorf("catalog: tool manifest: %w", err)
	}
	tool, err := doc.Spec.toCore(doc.Metadata.Name)
	if err != nil {
		return core.Tool{}, fmt.Errorf("catalog: tool manifest: %w", err)
	}
	if err := ValidateToolManifest(tool); err != nil {
		return core.Tool{}, err
	}
	return tool, nil
}

func LoadSkillManifestFile(path string) (core.Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Skill{}, err
	}
	return LoadSkillManifest(data)
}

func LoadSkillManifest(data []byte) (core.Skill, error) {
	var doc SkillDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return core.Skill{}, fmt.Errorf("catalog: skill manifest: %w", err)
	}
	skill, err := doc.Spec.toCore(doc.Metadata.Name)
	if err != nil {
		return core.Skill{}, fmt.Errorf("catalog: skill manifest: %w", err)
	}
	if doc.Metadata.Version != "" {
		skill.Version = doc.Metadata.Version
	}
	if err := ValidateSkillManifest(skill); err != nil {
		return core.Skill{}, err
	}
	return skill, nil
}
