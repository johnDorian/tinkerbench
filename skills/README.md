# Agent skills

Skills that teach a coding agent (Claude Code or any agent that reads `SKILL.md` folders) how to use the three workbenches well over MCP. The servers' own MCP instructions cover the tools; these add the workflow and the lessons learned.

| Skill | For |
|---|---|
| [openscad-workbench](openscad-workbench/SKILL.md) | Writing and rendering models, `part()`/`feature()`, versions, working through comments |
| [scad-printability](scad-printability/SKILL.md) | Proving every feature above the bed is supported, before a model is called done |
| [scad-copy-part](scad-copy-part/SKILL.md) | Copying a real part from a scan or photo with `measure_part`, asking for what can't be measured |
| [fritzing-workbench](fritzing-workbench/SKILL.md) | Building and checking breadboard/schematic/PCB sketches |
| [orcaslicer-print](orcaslicer-print/SKILL.md) | Importing, slicing and printing, with the confirm rules for the real printer |

## Install

They live outside `.claude/` so they can be shared with any project that uses the workbenches. Link them into your personal skills (all projects):

```sh
task skills:install    # symlinks every skill into ~/.claude/skills, skipping names that already exist
```

or into one project only:

```sh
mkdir -p .claude/skills && for d in /path/to/tinkerbench/skills/*/; do ln -s "$d" .claude/skills/; done
```

The skills expect MCP servers named `openscad`, `fritzing` and `orcaslicer` (see the main [README](../README.md#quick-start)).
