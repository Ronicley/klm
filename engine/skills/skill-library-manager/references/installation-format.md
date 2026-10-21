# KLM user skill format

The supplied library root belongs to the operating-system user running the engine.
Default: `~/.klm/skills`. Each immediate subdirectory is one skill:

```text
<library-root>/example-skill/
  SKILL.md
  references/
  scripts/
  assets/
```

`SKILL.md` must be UTF-8 with YAML frontmatter containing `name: example-skill`
and a nonempty `description`. The name must equal the lowercase directory slug:
letters, digits and single hyphens, at most 64 characters. Windows reserved names,
`skill-library-manager` and `klm-internal-*` are reserved. Use ordinary portable
files/directories, no symlinks, junctions, device files, traversal or absolute paths.
One package is limited to 512 files, 16 MiB, 16 directory levels; SKILL.md is limited
to 256 KiB. References are part of the package, not separate skill installations.

Keep scripts as files; copying them is not authorization to execute them. Stage
outside the library and publish the validated complete folder with rename. An
existing different folder requires the user's explicit replacement decision.
