# Changelog

## [0.1.3](https://github.com/drewdrewthis/git-orchard-rs/compare/v0.1.2...v0.1.3) (2026-04-04)


### Bug Fixes

* use correct git+https repository URL format in npm package ([#132](https://github.com/drewdrewthis/git-orchard-rs/issues/132)) ([a049444](https://github.com/drewdrewthis/git-orchard-rs/commit/a049444b78d4b7b96602436da6c8b4e9d396e29f))

## [0.1.2](https://github.com/drewdrewthis/git-orchard-rs/compare/v0.1.1...v0.1.2) (2026-04-04)


### Bug Fixes

* switch x86_64-apple-darwin to macos-14 runner ([#128](https://github.com/drewdrewthis/git-orchard-rs/issues/128)) ([8ad2aba](https://github.com/drewdrewthis/git-orchard-rs/commit/8ad2aba74bac768a61ba0051642fc2d0169dcfec))

## [0.1.1](https://github.com/drewdrewthis/git-orchard-rs/compare/v0.1.0...v0.1.1) (2026-04-04)


### Bug Fixes

* correct softprops/action-gh-release SHA (v2.6.1) ([#126](https://github.com/drewdrewthis/git-orchard-rs/issues/126)) ([3a86005](https://github.com/drewdrewthis/git-orchard-rs/commit/3a8600549585ba935052bec25225712a84fc361b))

## 0.1.0 (2026-04-04)


### Features

* add --version/-V flag ([#49](https://github.com/drewdrewthis/git-orchard-rs/issues/49)) ([cf183db](https://github.com/drewdrewthis/git-orchard-rs/commit/cf183db6c4c3a7bdc285cffb922d5cb3f1c6dd3e)), closes [#33](https://github.com/drewdrewthis/git-orchard-rs/issues/33)
* add /install-orchard skill for new user onboarding ([ac17505](https://github.com/drewdrewthis/git-orchard-rs/commit/ac17505268c1e152b64acf6aba1ef9e427300051))
* add `i` keybinding to open linked GitHub issue in browser ([31707fd](https://github.com/drewdrewthis/git-orchard-rs/commit/31707fd78e1bf5f8e44176e9a0b805b2d64ba97b)), closes [#4](https://github.com/drewdrewthis/git-orchard-rs/issues/4)
* add debug logging throughout codebase (ADR-002) ([0b0b6b5](https://github.com/drewdrewthis/git-orchard-rs/commit/0b0b6b52b7227a364d4fe609dee4420f62e1feb5))
* add i keybinding to open linked GitHub issue in browser ([ae88f5e](https://github.com/drewdrewthis/git-orchard-rs/commit/ae88f5e68b00867ab177ae608151e9ea9f761a79))
* add integration test suite (33 new tests) ([83f96b2](https://github.com/drewdrewthis/git-orchard-rs/commit/83f96b2ec246fcf4d89f1cc1a1b0df7056ee08e6))
* add integration test suite with 33 new tests ([#3](https://github.com/drewdrewthis/git-orchard-rs/issues/3)) ([9615a1f](https://github.com/drewdrewthis/git-orchard-rs/commit/9615a1f6c4184f5b6612a69789b7cb5557f89ef9))
* add mouse support to TUI ([#77](https://github.com/drewdrewthis/git-orchard-rs/issues/77)) ([ad342ea](https://github.com/drewdrewthis/git-orchard-rs/commit/ad342ea0260fe0ed9abeb793edb75905b8f66a84))
* add orchard heal command for self-repair and cleanup ([#16](https://github.com/drewdrewthis/git-orchard-rs/issues/16)) ([#71](https://github.com/drewdrewthis/git-orchard-rs/issues/71)) ([3952ce1](https://github.com/drewdrewthis/git-orchard-rs/commit/3952ce14cca89909eeaa47e841dcaa29ba3a5598))
* add panic hooks to prevent terminal corruption on crash ([#66](https://github.com/drewdrewthis/git-orchard-rs/issues/66)) ([1454366](https://github.com/drewdrewthis/git-orchard-rs/commit/1454366f5f3b96119029e7b3175d961b0f72ce56))
* add setup-remote command to provision remote hosts ([#69](https://github.com/drewdrewthis/git-orchard-rs/issues/69)) ([d5e1231](https://github.com/drewdrewthis/git-orchard-rs/commit/d5e1231196484769db6405894fe51dcbad74cc45))
* add Theme struct to centralize TUI styling ([#67](https://github.com/drewdrewthis/git-orchard-rs/issues/67)) ([8a46f7d](https://github.com/drewdrewthis/git-orchard-rs/commit/8a46f7dace14e2c570fe47c5931d6ee404093099))
* add TUI keybinding to create new worktrees ([#57](https://github.com/drewdrewthis/git-orchard-rs/issues/57)) ([#73](https://github.com/drewdrewthis/git-orchard-rs/issues/73)) ([c3ab9d0](https://github.com/drewdrewthis/git-orchard-rs/commit/c3ab9d07a149ddedf6bf0b393c36d27858205be0))
* add TUI screenshot test helper ([#75](https://github.com/drewdrewthis/git-orchard-rs/issues/75)) ([#76](https://github.com/drewdrewthis/git-orchard-rs/issues/76)) ([a886e0d](https://github.com/drewdrewthis/git-orchard-rs/commit/a886e0dc4b68fb82df1c86b353fb5982289446ca))
* adopt ratatui ecosystem widgets for TUI polish ([#56](https://github.com/drewdrewthis/git-orchard-rs/issues/56)) ([#78](https://github.com/drewdrewthis/git-orchard-rs/issues/78)) ([be3f6e9](https://github.com/drewdrewthis/git-orchard-rs/commit/be3f6e952ac4e82ff595c696487dbdbbfd25354c))
* auto-create main tmux session at worktree origin on TUI startup ([#2](https://github.com/drewdrewthis/git-orchard-rs/issues/2)) ([4f1b5e7](https://github.com/drewdrewthis/git-orchard-rs/commit/4f1b5e7b85214153e58a44b60187d6f5c02e00b5)), closes [#1](https://github.com/drewdrewthis/git-orchard-rs/issues/1)
* Claude hooks state detection, branch column, auto-session creation ([5cf4742](https://github.com/drewdrewthis/git-orchard-rs/commit/5cf474225cdcd3c1003d391c20e3b9f162ebeba3))
* click notification to open Warp and switch tmux session ([892e517](https://github.com/drewdrewthis/git-orchard-rs/commit/892e5174ce21b9328ae23162ee681f9fe1b6ba17))
* expandable pane sub-rows with column reorder ([#97](https://github.com/drewdrewthis/git-orchard-rs/issues/97)) ([a2f1ab6](https://github.com/drewdrewthis/git-orchard-rs/commit/a2f1ab62ecb5d762cfef9a19148bfca278ba12c1))
* fetch Claude state files from remote hosts over SSH ([#72](https://github.com/drewdrewthis/git-orchard-rs/issues/72)) ([92a4e8c](https://github.com/drewdrewthis/git-orchard-rs/commit/92a4e8cf951bfcb053114653c5e3c17fd0848eaf))
* focused default view — collapse backlog, filter, toggle columns ([#14](https://github.com/drewdrewthis/git-orchard-rs/issues/14)) ([4e65266](https://github.com/drewdrewthis/git-orchard-rs/commit/4e65266c867f8dd7294203e3262ee5eb79837695))
* full Rust + Ratatui rewrite of git-orchard ([d107b0f](https://github.com/drewdrewthis/git-orchard-rs/commit/d107b0f5cca4f81d2bfb5ee87305b33bae6b753a))
* make terminal app configurable via global config and init wizard ([#52](https://github.com/drewdrewthis/git-orchard-rs/issues/52)) ([8580e78](https://github.com/drewdrewthis/git-orchard-rs/commit/8580e785c324c8fb2851cc44e87cc9ba158efdcb))
* popup mode, task-centric TUI, and service cache architecture ([a9563d5](https://github.com/drewdrewthis/git-orchard-rs/commit/a9563d59dfb5072698089dc6b386832a1b25bee2))
* remove backlog collapse, add priority toggle, left/right repo nav ([3fb994a](https://github.com/drewdrewthis/git-orchard-rs/commit/3fb994a4815d604fe41d565844681408b5783cd7))
* remove legacy collector, TUI fully driven by build_state ([#22](https://github.com/drewdrewthis/git-orchard-rs/issues/22) phase 3) ([bbc2b41](https://github.com/drewdrewthis/git-orchard-rs/commit/bbc2b41b949afa1e19515f590de3c7abe756d4f5))
* render standalone shepherd sessions in TUI ([#81](https://github.com/drewdrewthis/git-orchard-rs/issues/81)) ([#82](https://github.com/drewdrewthis/git-orchard-rs/issues/82)) ([2aee418](https://github.com/drewdrewthis/git-orchard-rs/commit/2aee418f30465166128c217bb4593d04ae059c80))
* restore original logo, add ❤ footer, remove filters, upgrade borders ([e84b68a](https://github.com/drewdrewthis/git-orchard-rs/commit/e84b68aecd1ba0ba4e15f6fec35c3eb3d561d6ec))
* shepherd persistent global session ([#47](https://github.com/drewdrewthis/git-orchard-rs/issues/47)) ([#68](https://github.com/drewdrewthis/git-orchard-rs/issues/68)) ([e5bc127](https://github.com/drewdrewthis/git-orchard-rs/commit/e5bc127709ccdf1b204c6964ee29ea792fe159bc))
* show Claude Code activity indicator in status badge ([7eb3ab9](https://github.com/drewdrewthis/git-orchard-rs/commit/7eb3ab9894ed8cbeaaae75ba31e1c1a5fc774756))
* task-centric TUI with cache architecture and multi-repo support ([a7ca2f6](https://github.com/drewdrewthis/git-orchard-rs/commit/a7ca2f67781748369f8a378ec792fa007d68cf48))
* unified OrchardState data model with build_state compositor ([#22](https://github.com/drewdrewthis/git-orchard-rs/issues/22)) ([5ec21b6](https://github.com/drewdrewthis/git-orchard-rs/commit/5ec21b6956f1f07770037070ce90a8617ebd1686))
* wire --json through build_state, consolidate cache reading ([#22](https://github.com/drewdrewthis/git-orchard-rs/issues/22) phase 2) ([23e4003](https://github.com/drewdrewthis/git-orchard-rs/commit/23e400357519326bb380bc594fc612024b2b11d9))
* workspace tab bar with colored repo indicators ([#91](https://github.com/drewdrewthis/git-orchard-rs/issues/91)) ([a5e3e53](https://github.com/drewdrewthis/git-orchard-rs/commit/a5e3e532ca9b4c8e6ae97fbbfd5a1b6e8cb8c0f9))


### Bug Fixes

* add clone step to install-orchard skill ([cf565d3](https://github.com/drewdrewthis/git-orchard-rs/commit/cf565d345eae178cd1ee02a71b29aeb36084bac0))
* add key event and tmux switch debug logging ([8df9ec1](https://github.com/drewdrewthis/git-orchard-rs/commit/8df9ec14a1605c5fd01d5fc400bde4a8bb2c34c4))
* allow tilde in shell_escape safe characters ([1bf3964](https://github.com/drewdrewthis/git-orchard-rs/commit/1bf39648d39ae553276b7d0b36827244429b8511))
* capture stderr from remote tmux switch-client for better error messages ([bc615b7](https://github.com/drewdrewthis/git-orchard-rs/commit/bc615b7574f87257118334e6cfd750ef0093b4c3))
* cleanup dialog shows progress spinner and deletion results ([ef38cef](https://github.com/drewdrewthis/git-orchard-rs/commit/ef38cef46452c8a2c7519411ead905c8df3d2d21))
* critical bugs from code review ([b2bfc0b](https://github.com/drewdrewthis/git-orchard-rs/commit/b2bfc0bf7b373b7baaf6c80993393c3aae1ce021))
* Ctrl+C exits with code 130 to break the shell restart loop ([59284c5](https://github.com/drewdrewthis/git-orchard-rs/commit/59284c5114f58220f49b0525dbcebc8f26a981a4))
* dim remote rows until connectivity is confirmed ([a7d4213](https://github.com/drewdrewthis/git-orchard-rs/commit/a7d4213c27ac3251a6dc3424e92274472e64e61d))
* fix release-please workflow and add npm wrapper package ([#124](https://github.com/drewdrewthis/git-orchard-rs/issues/124)) ([ffe859f](https://github.com/drewdrewthis/git-orchard-rs/commit/ffe859f4900dfa0e0fbfcf0073d9ea62e314155a))
* forward CLI command arg through tmux popup re-launch ([#85](https://github.com/drewdrewthis/git-orchard-rs/issues/85)) ([3ad7622](https://github.com/drewdrewthis/git-orchard-rs/commit/3ad762283e9c787b003a19b26c71131238e8ddbb))
* guard macOS-only notification code with cfg attrs ([#50](https://github.com/drewdrewthis/git-orchard-rs/issues/50)) ([0ba4540](https://github.com/drewdrewthis/git-orchard-rs/commit/0ba454024122ccb0aa1a178c6fa06f584c1279ca))
* issues cache — remove assignee filter, fetch all states ([#21](https://github.com/drewdrewthis/git-orchard-rs/issues/21)) ([cb9603c](https://github.com/drewdrewthis/git-orchard-rs/commit/cb9603c7d18ab0d987ae2cdd2586eb7fe8b8ee5b))
* logo rendering, preview height, ANSI escape codes, refresh behavior ([99843cf](https://github.com/drewdrewthis/git-orchard-rs/commit/99843cf7b27b21daac70df04a0f6cd4e8f9046c2))
* preserve remote worktrees during refresh, normalize paths, improve status display ([03cd93b](https://github.com/drewdrewthis/git-orchard-rs/commit/03cd93b1795d39a924827db415453b3682ac9f15))
* q switches back to previous tmux session instead of quitting ([e4cbf37](https://github.com/drewdrewthis/git-orchard-rs/commit/e4cbf37bb4613d8c415e26aca0a0dea43866ed0b))
* remove alternate screen so tmux switch-client works seamlessly ([ad1f3bf](https://github.com/drewdrewthis/git-orchard-rs/commit/ad1f3bf9f368550ffb47e55fbeee79c9b6cf8f0a))
* remove while-true loop so q actually quits the TUI ([54bc939](https://github.com/drewdrewthis/git-orchard-rs/commit/54bc9396f5522fb6f89da81d36229997051d7381))
* replace hardcoded /tmp paths with system temp directory ([#41](https://github.com/drewdrewthis/git-orchard-rs/issues/41)) ([0f81549](https://github.com/drewdrewthis/git-orchard-rs/commit/0f81549735616fc7448c54febb86a1c36080aae2)), closes [#29](https://github.com/drewdrewthis/git-orchard-rs/issues/29)
* restore 'p' key to priority toggle, remove Transfer feature ([#102](https://github.com/drewdrewthis/git-orchard-rs/issues/102)) ([794f880](https://github.com/drewdrewthis/git-orchard-rs/commit/794f880658f3f50f0d977b451304b666fa38d993))
* show closed issues and merged PRs in status, sort to needs attention ([4474073](https://github.com/drewdrewthis/git-orchard-rs/commit/4474073ef7a14459f996e4985f03f139d3d6d1be))
* skip default branches in PR matching, populate issue_state ([b8fee0b](https://github.com/drewdrewthis/git-orchard-rs/commit/b8fee0bcb9a14bc77aaf882d273f828496b31b2e))
* suspend terminal before tmux switch so session switching works ([40a9be0](https://github.com/drewdrewthis/git-orchard-rs/commit/40a9be0a921e338c97b5d4367803312aacd2f650))
* tmux session switching runs in background, shows warning on failure ([ba9ae11](https://github.com/drewdrewthis/git-orchard-rs/commit/ba9ae11e163a05ef01464740323d41721d55a0e2))
* treat null reviewDecision as no review required ([2ea6c8a](https://github.com/drewdrewthis/git-orchard-rs/commit/2ea6c8a5a618c541a5a8663a3b2b8b7e0eba7317))
* treat null reviewDecision as no review required ([#6](https://github.com/drewdrewthis/git-orchard-rs/issues/6)) ([8413af7](https://github.com/drewdrewthis/git-orchard-rs/commit/8413af76f8ddfd83fe102bd69a0d0eedd5a3a757))
* update test assertions after PII scrub from git history ([2ca05b0](https://github.com/drewdrewthis/git-orchard-rs/commit/2ca05b08610a2bd4939bd8c7ffaec8697d89305f))
* use shell_escape for session name in notification execute command ([#42](https://github.com/drewdrewthis/git-orchard-rs/issues/42)) ([2a74110](https://github.com/drewdrewthis/git-orchard-rs/commit/2a74110ab10b3d58518727ec14bd451c25198df2)), closes [#32](https://github.com/drewdrewthis/git-orchard-rs/issues/32)
