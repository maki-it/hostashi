# Setting up the Claude project

Created in claude.ai, takes about 3 minutes:

1. In claude.ai, on the left, **Projects → New project**. Name: `Hostashi`. Description for example "Open source hosting control panel, alternative to CloudPanel".
2. **Set project instructions**: paste the content of [`PROJECT-INSTRUCTIONS.md`](PROJECT-INSTRUCTIONS.md).
3. **Add content → Upload files**: upload these eight files:
   - `../README.md`
   - `../docs/01-cloudpanel-research.md`
   - `../docs/02-vision-scope.md`
   - `../docs/03-features.md`
   - `../docs/04-architecture.md`
   - `../docs/05-security.md`
   - `../docs/06-roadmap.md`
   - `../docs/07-decisions.md`
4. Start a new chat in this project, for example with: "Here are my answers to the open questions from 07-decisions: …"

## Maintenance

A project chat cannot change the knowledge files itself. The state is only kept if you upload changed files again. The instructions make sure that Claude names the necessary changes at the end of every session. The originals stay in this Git repository, which remains the source of truth.

The menus in claude.ai change from time to time. If the labels differ, look for "Projects" in the sidebar.
