package docs

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"deck2/internal/registry"
)

// SkillsDir is where skill documents live; a skill is the folder skills/<name>/ with SKILL.md in it.
const SkillsDir = "skills/"

// SkillOf is the skill a document name belongs to, or "" when it is not under skills/<name>/.
func SkillOf(name string) string {
	rest, ok := strings.CutPrefix(name, SkillsDir)
	skill, file, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || file == "" {
		return ""
	}
	return skill
}

// checkSkillText checks that skill files sit in a skill folder and SKILL.md carries the AgentSkills frontmatter.
func checkSkillText(kind, name, text string) error {
	skill := SkillOf(name)
	if (kind == KindSkill) != (skill != "") {
		return registry.Invalid("deck: skill files are named skills/<skill>/<file>, and only skill files are")
	}
	if name != SkillsDir+skill+"/SKILL.md" {
		return nil
	}
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return registry.Invalid("deck: SKILL.md must start with YAML frontmatter between --- lines")
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(front[:end]), &meta); err != nil || meta == nil {
		return registry.Invalid("deck: SKILL.md frontmatter is not a YAML mapping")
	}
	for key := range meta {
		if k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-"); k == "allowed-tools" || k == "hooks" {
			return registry.Invalid("deck: SKILL.md may not carry %q: tools and their rights come from the deck gateway", key)
		}
	}
	if d, _ := meta["description"].(string); strings.TrimSpace(d) == "" {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"description\"")
	}
	if n, _ := meta["name"].(string); n != skill {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"name: %s\", the name of its folder", skill)
	}
	return nil
}
