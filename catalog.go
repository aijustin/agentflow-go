package agentflow

import (
	"github.com/aijustin/agentflow-go/pkg/catalog"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// LoadToolManifestFile loads and validates a standalone tool catalog manifest.
func LoadToolManifestFile(path string) (core.Tool, error) {
	return catalog.LoadToolManifestFile(path)
}

// LoadToolManifest loads and validates a standalone tool catalog manifest document.
func LoadToolManifest(data []byte) (core.Tool, error) {
	return catalog.LoadToolManifest(data)
}

// ValidateToolManifest validates a tool manifest for catalog registration.
func ValidateToolManifest(tool core.Tool) error {
	return catalog.ValidateToolManifest(tool)
}

// LoadSkillManifestFile loads and validates a standalone skill catalog manifest.
func LoadSkillManifestFile(path string) (core.Skill, error) {
	return catalog.LoadSkillManifestFile(path)
}

// LoadSkillManifest loads and validates a standalone skill catalog manifest document.
func LoadSkillManifest(data []byte) (core.Skill, error) {
	return catalog.LoadSkillManifest(data)
}

// ValidateSkillManifest validates a skill manifest for catalog registration.
func ValidateSkillManifest(skill core.Skill) error {
	return catalog.ValidateSkillManifest(skill)
}
