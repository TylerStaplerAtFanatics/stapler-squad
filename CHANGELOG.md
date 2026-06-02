# Changelog

## [1.31.1](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.31.0...v1.31.1) (2026-06-02)


### Bug Fixes

* **lint:** gofmt rules_service_test.go ([cd3c4b1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/cd3c4b1628e9b8ffa6daf487f31695573b7bf8ec))

## [1.31.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.30.0...v1.31.0) (2026-06-02)


### Features

* add support for Antigravity (agy) CLI ([a981bcb](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a981bcb16db6ee557c9a487b48703951c48d2cdd))
* **agy:** full Antigravity CLI support — hooks, install, UI, detection ([e66d058](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e66d05839789737cabad6d8b9401c3b431aff9e8))
* **analytics:** program detail panel with subcommand drill-down ([#85](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/85)) ([9d43582](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/9d4358272d7293457c6f6474480d66f4bd4b2341))
* **backlog:** automated triage pipeline with review panel ([fcafaed](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/fcafaedd9bdb2a01e8765d44d9d7324df0a80ec8))
* **backlog:** session monitor panel and backlog detail improvements ([80d3f3b](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/80d3f3bf60fbb949c549265055a9855e43a631b6))
* **backlog:** triage re-trigger fix, detail UI polish, backlog in mobile nav ([6a174c3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6a174c3a0e1298bec2cc387b31de51f5c0eb7ecd))
* **backlog:** workflow engine and status event audit log ([7458bcb](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/7458bcb33d42d98fa2602c8caaab44cd402b5106))
* **headless:** session/headless pool — cache-optimized background LLM calls via claude -p ([fba787e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/fba787e5c843848fceb76eb93654bf03ab8748a0))
* **headless:** session/headless pool — cache-optimized background LLM calls via claude -p ([0a4e10e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/0a4e10e7714c495375468a46c5da7bc44d712b87))
* **mcp:** tag MCP-created sessions with source:mcp ([e3191bc](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e3191bc271dbac302c944ee95e6c7f2ef5aaf36d))
* **memory:** surface memory pressure and fix hibernation sweeper ([f455f3b](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f455f3bf655eea20690c3a5cdfdcb8ce5c132d5f))
* **memory:** surface memory pressure and fix hibernation sweeper ([5632d7e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/5632d7eacfcc47742b300b7b8ad097d7f0e0ed83))
* **memory:** surface memory pressure and fix hibernation sweeper ([2927de7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/2927de74739889a092d796e11b19190d7516ba2f))
* **rules:** modal dialog, scrollable table, analytics deep-link pre-fill ([6f654e3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6f654e30e9b721c8ab1f36dc6522a38093bc56dc))
* **rules:** UX improvements — toast z-index, mobile layout, form sections, table clarity ([17b2a6f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/17b2a6f20d4ef2e110c11d2356bc60d42c46eb14))
* **rules:** YAML import/export and UX improvements for ApprovalRulesPanel ([#123](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/123)) ([ea92de7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ea92de7fc83f8b060eabb2d5425c9f13233bc1cd))
* **session:** immortal migration — ProcessManager interface + TmuxBackend + NativeProcessManager ([#113](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/113)) ([66b4c65](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/66b4c65179ce444bcedb14f97af87d1cb90020fb))
* **session:** session steering — supervised driver for all automated sessions ([874c1e6](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/874c1e63409953706378430326570fc2491112d4))
* **session:** WriteToSession RPC for sending raw input to a PTY ([5b528f1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/5b528f1a015d202412b10084d0c41553f42d9c19))
* **shell-tabs:** complete UX discovery, error states, and full test coverage ([#93](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/93)) ([c380068](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c380068e34ff3e6772d9e1b9a4ee8bf5c092f9ca))
* **shells:** custom shell tabs — interactive PTY shells attached to sessions ([#109](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/109)) ([fb5a6d1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/fb5a6d15d0e29b7e52cadf89412fac3f74ffc1c2))
* **upload:** multi-file + Android camera/gallery/file picker in terminal toolbar ([b4b43e4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b4b43e4d04d3f9bc2281ae95765304873338e2ba))
* **upload:** multi-file + Android camera/gallery/file picker in terminal toolbar ([417ac0a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/417ac0a2c8821c18aa09f11cd7dcde0d021b5922))
* **ux:** paused session clarity — overlay, visual distinction, pause reason ([#90](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/90)) ([d07584c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d07584cd95b6fb7a5f630a9db019de8d457b1ec3))


### Bug Fixes

* address code review findings from history-linker and workspace-service ([e80cb2a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e80cb2aea4bd9369b68ca52b83bd856b462002a9))
* **backlog:** repair triage pipeline — prompt injection, session exit, and oneshot flag ([cbc938b](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/cbc938bd4927ba6c6145148a85ad8cf948be08ab))
* **ci:** add ent ORM generation step before lint and build ([81e7ebf](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/81e7ebfb627186e5e3feaf89ec317594df2a351c))
* **ci:** allow ESLint warnings in full-scan path (push/workflow_dispatch) ([94b273c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/94b273ceb21252dd87ee451ebc747b4ac08fcf0e))
* **ci:** include session/ent/ in build artifact for test and cross-compile jobs ([3d823f1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3d823f1169182cb82204649505610d67c77c668f))
* **detection:** detect ✻ asterism prefix as Claude active state ([#114](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/114)) ([a339b29](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a339b29c64117e3ec647f7a901a727562b79646a))
* **headless:** resolve all review findings — atomicity, security, concurrency, test coverage ([6f2cb76](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6f2cb76fb287820f34f38b9a5113eb89326c523d))
* **lint:** remove embedded ReviewState selector in sweeper test (QF1008) ([3fbcac9](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3fbcac9f25cee49d421a9be8e2332acc501f94a8))
* **lint:** remove redundant embedded field selector in sweeper test ([7b437d8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/7b437d8037d873a8b2fe411cda950df9a50efc9f))
* **lint:** resolve prealloc violations and remove unused shellWgWait ([29c52c5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/29c52c5809efa20d0b830c3211850a61be95e161))
* **lint:** use LiveInstancesProvider and Active instead of deprecated LoadInstances/Running ([a99d967](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a99d967fdb023888e9d1fba4efb560a6fb82e597))
* **log:** prevent panic on service restart; fix ESLint violations ([38f666f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/38f666f0665ba7bc042deb6f9d9a66c97a02e735))
* **macos:** stable TCC permission grants via self-signed codesigning ([#112](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/112)) ([3cc037f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3cc037fb0eaa3f151d2acfdc6ca04b72e5d7e113))
* **memory:** address code review findings — cache I/O, RSS coverage, error UX ([8cc95ba](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/8cc95ba0f6b5e93296af2549d0e84126619397f8))
* **memory:** fix cache population, pause guard, and error UX for hibernate/pause ([27b5584](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/27b5584bd421acdc03cbf0de811aa333f149eff6))
* **notifications:** condition-change gating for health alerts + native notification lifecycle ([#92](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/92)) ([c9b08c5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c9b08c5241bf6c48059df3f1fbd2beaa2db51228))
* **notifications:** fix type mapping, approval guard, and test enum values ([#110](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/110)) ([c4b2ee3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c4b2ee346b76c74cf38db3bb52ae273b74b0ee48))
* post-merge build repairs for sync/upstream-20260528 ([dea6a86](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/dea6a869ca86668d147914fcd27b8c9a96899046))
* **session:** fix two data races and semaphore ordering from upstream merge ([5f3208a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/5f3208a82425aa0323794faae1c968b4e60039f4))
* **session:** re-detect conversation UUID after /clear ([3ec93c4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3ec93c43ae0a307099b1f99c7592c02965d41bde))
* **session:** replace inst.tmuxManager.GetTmuxSessionName() with inst.GetTmuxSessionName() ([3651044](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3651044a4783502d1c17e664e57b0264653df4e7))
* **session:** replace time.Sleep with os.Chtimes in HistoryLinker tests to fix mtime race ([b731b6a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b731b6a560bb74580d7d5841ea95eea7010277e1))
* **session:** replace undefined NeedsApproval with detection layer check ([df56305](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/df563054c15be35f1983a7fc2b1baf969019e170))
* **sessions:** cascade session deletion to review queue, notifications, and modal auto-advance ([ce14e75](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ce14e7586aa48a1e3789a42d3adfb194575f8e10))
* **test:** use Stopped status in seedInstance to avoid tmux spawn in LoadInstances ([6b2f9d9](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6b2f9d92ba2d9a9b7204ab54e17e6abae38a909b))
* **tokens:** use UTC in IsStale() to avoid timezone boundary false-positives ([b0cb29d](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b0cb29d294271e7cbe1bceb7db300111a7d66b0c))
* **ui:** rename Gallery button to Images in terminal upload toolbar ([a23a124](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a23a124cbce4507c13001f7ffdf2e9df23b889d4))
* **ui:** show pane header tab strip on mobile (remove display:none at ≤768px) ([0eae9e6](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/0eae9e61b392cc6b96bb533e5cf6e5c2382bdb64))
* **upload:** extract FileList to array before input reset to fix Android upload ([c2ba4aa](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c2ba4aa156036a38143a0a029ff3c5845a635466))
* **upload:** fix stale upload tests and add disconnected-state UX fallback ([ebb8a1b](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ebb8a1b3080babb41ef995c861d9386690f1ba02))


### Performance Improvements

* debounce omnibar Fuse searches and pre-sort sessions selector ([3231d75](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3231d75a5942fe77cc3bdf7d8cef99c90b125d15))
* eliminate hot-path allocs and reduce JS bundle size ([#107](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/107)) ([7e14c40](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/7e14c407435b01f312038fac1b7b3a933e595a84))
* eliminate LoadInstances per-request in WorkspaceService and stagger git-status bursts ([dc99f6d](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/dc99f6d79823bdc2c7c7ea56f52bcdd92b4c38dc))
* hot-path alloc elimination, go-git low-alloc index scan, JS bundle reduction ([#111](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/111)) ([14cc562](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/14cc562140030c42635d2071af0c61e3fd011dcf))
* reduce goroutine wakeups, lazy-load xterm.js, memoize SessionCard, direct SQL updates ([#122](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/122)) ([4104b01](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/4104b01dec64aeecc91d7a49e2b09437b32b3204))
* remaining enforcement tests + CI coverage ([e8c150f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e8c150f5dee4a3b1928a401dde48165998203a03))
* **session:** lock-free GetTimeSinceLastMeaningfulOutput via atomic shadow ([22d1de5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/22d1de574786e04f038a38b0f11c103a4070b9fe))
* **session:** lock-free shell registry + allocation hot-path fixes ([8b883f3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/8b883f37052af83299ef4f3752f2e487813a2f59))
* TTL cache for DiffShortstat + zero-alloc enforcement tests ([98d503c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/98d503c756cb2b71f0e5e374fc2af869ef1d1df6))

## [1.30.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.29.0...v1.30.0) (2026-05-18)


### Features

* **insights:** token & spend monitoring dashboard with model-over-time chart ([#104](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/104)) ([6b682cb](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6b682cbc02f25dcda630cce13724b38f5d94b01a))

## [1.29.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.28.0...v1.29.0) (2026-05-18)


### Features

* **backlog:** add full backlog management layer ([#74](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/74)) ([765b904](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/765b9049729d80d14181a228d23205b1c622bdd1))
* **files:** resizable tree panel, mobile layout, recent files, quick-open palette ([#81](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/81)) ([78ecb19](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/78ecb19c9e547de968c2ed107964574aba0bcc68))
* **mobile:** mobile UX fixes, Go concurrency fixes, reflect-and-fix enforcement ([790d10c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/790d10c01ead2a5a9a3affd9bada79a10203fbed))
* **mobile:** pane header mobile fixes and session row layout improvements ([b4e841e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b4e841e61814d6d1d195839031bf4cf9f0ff48d5))
* **ui:** dark slate theme, compact rows, onboarding modal, /help hub ([#79](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/79)) ([cf5b838](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/cf5b8380a4a75cfc416aa8102271ca890c64c5c8))
* **upload:** generalize file upload to accept any file type with chip UI ([#80](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/80)) ([fb70cf3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/fb70cf315f4dd04156f2b0a5f6984c52ac7d923e))


### Bug Fixes

* **analytics:** serialize events as snake_case integers to fix null duration_ms ([0c2b35e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/0c2b35ee534fc4cdfbd3331882983c193962b3d3))
* **mobile:** keyboard visible on foldable widths; restore overflow menu in row view ([13c7950](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/13c7950b6ae38024c261062ae0f6bd0a75bd7b4a))
* **sessions:** pass full action set to SessionRow in row view mode ([58d8aa3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/58d8aa3f44ebba1be81bb4c29e57907955c73ef0))
* **tests:** use meaningful alt text on FileChipList thumbnail to fix role queries ([e67f63d](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e67f63d7f6c323bd8d795d654e28f5460bf528b6))
* **theme:** add missing statusDot and transition tokens to contract ([9f045cf](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/9f045cfe2d8ba6813b1ce93a32896324384623b9))


### Performance Improvements

* eliminate forkExec lock contention from hot subprocess paths ([#102](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/102)) ([79582d4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/79582d49563347917065c73ee61d51dba5c9ac17))

## [1.28.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.27.0...v1.28.0) (2026-05-15)


### Features

* **analytics:** pluggable analytics system with SQLite storage and ESLint enforcement ([#69](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/69)) ([74f6f28](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/74f6f2819e1df1b64a4e0b45950a60dc7a0bfee0))
* **create-session:** opt-in to create directory + git repo when path is missing ([#43](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/43)) ([6e0ebe7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6e0ebe7fd0f56abfeb469c89055aaa2d5474d0c8))
* **events:** add sequence numbers and 1-hour catch-up replay to EventBus ([03bbfc7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/03bbfc7e0ac6158ee778bed206d021253c300ce9))
* **files:** PDF/video inline viewer and ranger-style keyboard navigation ([#71](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/71)) ([f753a86](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f753a86da0624aae2e2dfe1020cf35da9e134c03))
* **mobile:** add "+" pane button to mobile tab strip for split creation ([178d893](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/178d8937b729e083d099b826fda2c445d441726d))
* **pane:** add action bar with V/H keyboard shortcuts to pane picker ([9153f7e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/9153f7ef74dd81c2758b184a08c3f7945eba36d8))
* **pane:** keyboard-driven pane picker and smart session routing ([f7b437e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f7b437eba59bbc2661bdc675d1fbaa34bd99aa02))
* **pane:** open session in new pane via Alt+click, overflow menu, and omnibar button ([37cb863](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/37cb86374007afdff3b0f55f57ae711aa6ca1a33))
* **queue:** event-driven review queue — eliminate 2s polling lag for controller sessions ([#68](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/68)) ([c66db93](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c66db9383eda0f5ae5f8acdcc436429b68c99fd8))
* **terminal:** robust resize quiescence, scrollback, and mobile gestures ([#67](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/67)) ([57ae1c1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/57ae1c16fa8a1c681e4db7b3a2c04b6529ee2176))
* **ui:** replace emoji and unicode icons with Lucide components ([f97ed72](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f97ed72f5011ecebeed11953232c2f0103b7976e))
* **unfinished:** count untracked files in DiffShortstat; default to GoGitVCSReader ([d2fe92e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d2fe92e90a947b26677a96dc4feff3d94d1a8528))
* **unfinished:** VCSReader interface with CLI-git, go-git, and jj implementations ([e2c396c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e2c396ca7900747e9e75643219ab85304e84cac8))
* **ux:** cyberpunk theme system, cockpit layout & keyboard shortcuts ([#51](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/51)) ([e538e4b](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e538e4b9e1a5e4121d5d6a448a8a0bc11d7b3dc6))
* **ux:** notifications page, nav badge, and smart pane targeting ([49bae61](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/49bae61ba9e349cb99b3cceaf968a478065f4f31))
* **ux:** tmux-style tiling pane engine with resizable splits ([8b45f92](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/8b45f92dbdb1b45999fa4fa02d7b24285be016ce))
* **ux:** unified tmux-style tiling — any pane can show session list or detail ([c598ff1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c598ff16b193dfd59eedf27b6e1473455068c65e))
* **ux:** UX polish pass — session list split, toast, toolbar, card density ([123d24c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/123d24ce3f932b827bf4512cc7266b5a5b9e5a7c))


### Bug Fixes

* **a11y:** fix WCAG AA color-contrast violation in reset-layout button ([670fab2](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/670fab2e9164e165cfb34320385c10b35ca3aa51))
* **a11y:** suppress card fade animation under prefers-reduced-motion ([d0377bd](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d0377bdd343f402e88d60266e7a848a99a05c91b))
* address is-it-ready review findings ([e038ef6](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e038ef6b4beaed0967644b06fc511c96f17c6353))
* **analytics:** replace analytics-exempt with real track() calls in page.tsx ([ee0eb16](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ee0eb1661da746fd4e1859ed7e2be7116a7749d1))
* **build:** move vanilla-extract style to .css.ts file ([38a6806](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/38a6806e06c4acd7d30b72635540a31cc74396c1))
* **ci:** resolve pre-existing benchmark path and analytics lint failures ([77e2d40](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/77e2d4086177143d60200dbe2ce62018862d65e3))
* correct bugs found in second is-it-ready pass ([c83e653](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c83e6531e0f8dfa7c09d46783f75e1a27e6ac1c4))
* **deps:** address PR review comments ([64eafd5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/64eafd5e47246e5822c8af9a92d25500255f0e91))
* **deps:** fix InstanceReader docstring and remove duplicate StateStore init ([a9b9b75](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a9b9b75366389b8d1952742fa05b9c425d1ac30b))
* **executor:** bound all blocking test reads/waits with 10s timeout helpers ([b309d96](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b309d96ef56d30218529e6f9101c0629a06e761b))
* **executor:** bound stderr test with context timeout to prevent hang ([45b7444](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/45b7444abd42e7c9da3002346f4c3277fbcdaee3))
* **frontend:** resolve TS type error in SessionDetailView + add planning docs ([43bbef3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/43bbef364dba4e9c9666ff68e2ee0c7cdf68faa6))
* **lint:** resolve all pre-existing ESLint errors in full-scan mode ([b856ef8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b856ef8f3e3a974c9f22651350b466be03a366bd))
* **lint:** resolve all pre-existing ESLint warnings (react-hooks, a11y, next) ([b912a58](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b912a58456df500effcbb4a472fb08e1e9c07bd6))
* **lint:** update norawexec analyzer and add nolint directives to test helpers ([468a470](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/468a4709ebae20c454d0060b3338316c4d651453))
* **mobile:** comprehensive mobile UX improvements — pane switcher, toolbar, keyboard ([#61](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/61)) ([ec75da8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ec75da8e660751fbc436ea160f4bd0d1435f8ea6))
* **mobile:** restore scroll on sessions page by constraining leafContainer height ([b1d23e8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b1d23e8ec27e3dd4825678dcc4aa94def726d91b))
* **mobile:** session navigation, list scrolling, and bottom nav height ([#63](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/63)) ([c669e74](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c669e74482a28dc2daca31d755f74fe4dba0c04d))
* **mobile:** show session tab bar on mobile when embedded in pane ([20f92d1](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/20f92d15de6f1f2fb93b462fa0b512652f42e2e4))
* **nav:** show badge counts on icons when sidebar is collapsed ([#70](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/70)) ([e9582b8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e9582b81e87d91d84f07546019640007bd7648ca))
* **nav:** surface Notifications and Settings in desktop header bar ([4fc2a86](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/4fc2a8697915d54da7957eee4f0fefdbc6d72fde))
* **nav:** unify DrawerNav with NAV_PAGES — add Notifications, Settings, Unfinished ([21be016](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/21be0165b8b8d0d99e6495e3f8f7333be4b30044))
* **notifications:** publish EventApprovalResponse to event bus for cross-device sync ([133067f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/133067f8db226d714e3b7e7f36c1051142c25055))
* **notifications:** resolve session title in approval and question notifications ([b6e2b57](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b6e2b5705a3fe210d20f4b4a42773d909d0f1989))
* **pane:** picker always shown with 2+ panes; sessions moveable between panes ([fd1e010](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/fd1e0105762694580207f07705864341a4515718))
* **pane:** prevent double picker on sessions stream update ([339bb07](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/339bb07c82802085681096bd8d7d8bb41af67646))
* resolve all non-blocking review findings ([27602fb](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/27602fb5829f97d4fd49d617fa83ef2d149f991d))
* **review-queue:** evaluate controller sessions on every poll cycle ([#72](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/72)) ([910cd19](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/910cd193bef6fbb511eb4917347c39faa3fa5183))
* **startup:** enforce tmux-before-sessions ordering with proof token ([94420fc](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/94420fc92cc1b3ad8863691063957ae313cecc16))
* **startup:** wire notification store before subscriber + tiling pane fixes ([1523b0d](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/1523b0d3e2aed0152d72ce485d22feb65d89e452))
* **terminal:** correct snapshot rendering after resize ([3851540](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3851540dbe81cc839d260863df4bc79e04ac9b73))
* **terminal:** sync cursor position after snapshot replay and break resize oscillation ([347d991](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/347d991a581f6a059ffe9f584ae6dd2ae4ca7c19))
* **tests:** update type assertions, mock setup, and e2e fixtures ([c188efe](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c188efeaec144ade267ed0566552af5c9b5d7330))
* **test:** update makeStaleInstance to access cache via contentProvider ([ce627fb](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ce627fb0631485a7aa8ea63e6828062ecec01f83))
* **types:** make isValidTab a proper type predicate for SessionDetailTab ([bb3f99a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/bb3f99aada248cab6a69d67386072b0ff58c4d7c))
* **unfinished:** add --no-optional-locks to all scanner git commands to prevent index.lock contention ([28c1241](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/28c12412f310dff24061b24e05b869af9e12890b))
* **ux:** stop review queue page from spawning a competing WebSocket stream ([b1fff3d](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b1fff3d65306c3c8ebd006e9274a72cfd7aaf717))


### Performance Improvements

* fix all hotpolllog violations from InfoLog extension (PerfFix-2/3/4) ([b81da4e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b81da4e07780b0686f81a58a0811cefd9eac4ec1))
* **log:** async writer eliminates log mutex contention in hot poll loop ([11f2d3c](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/11f2d3c8b9e524e71757edf6dc62c1cfe192deae))

## [1.27.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.26.0...v1.27.0) (2026-05-06)


### Features

* **classifier:** safely parse multiline python -c blocks with # comments ([#97](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/97)) ([be05345](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/be05345332c65b431a656002f0223917ee0f3c47))
* **executor:** safe subprocess framework with zombie prevention and process group management ([4d03970](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/4d03970402070b9bd841962fe25a0ac24df0e155))
* **lint:** add norawexec lint analyzer and safeexec framework plans ([cf96301](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/cf963014f6d95290cbf94eac9371e8aa48f7ee77))
* **omnibar:** auto-populate session name, first prompt injection, inline shorthand ([#95](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/95)) ([58d94a7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/58d94a746d377fde1fb395bdca482747a1fc6bb7))


### Bug Fixes

* **control-mode:** eliminate 3s timeout race and blank terminal on dead session ([006b45a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/006b45a245ac6d5609166b946739ab13f856cb02))
* **executor:** resolve golangci-lint violations in new executor test files ([d6f154f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d6f154f7789dbc89d2193d523161f3be79a34ace))
* **session:** add WaitDelay to all missing subprocess calls to prevent zombie accumulation ([3224302](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/32243022110659eda4dbcbeea94a0464392e8dfc))

## [1.26.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.25.0...v1.26.0) (2026-05-05)


### Features

* **adr:** add ADR-010 frontend modularity + enforce with eslint-plugin-boundaries ([685eaba](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/685eaba154332bacf854edb954d3ba3d6f7418e4))
* **classifier:** add approval rules, parser fixes, and CommandPattern linter ([#93](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/93)) ([a809681](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a809681d6a0fbe1672143cc942db780768a2fecf))
* **classifier:** auto-allow gh api PR review workflow commands ([#46](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/46)) ([e996dce](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e996dce6d66a28b245963e60dd22dbc9ed8d4cd7))
* **debug:** stream browser console logs to server via LogClientEvents RPC ([efac73a](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/efac73abcf28c00b66f1b1dcc838a290528ba452))
* **engineering-excellence:** DI framework, error observability, CI hardening ([b2f692f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b2f692ff6384c834d2b81c1da8ec45bd4174b2bc))
* **engineering-excellence:** DI framework, error observability, CI hardening ([13ec9f3](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/13ec9f3e5237fe26754ebab557a7d0d7edcb8a1e))
* **file-tree:** performance, themes, and file browser enhancements ([#86](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/86)) ([3f25b10](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3f25b10a2e2877bcc60d0cbfd522bc2d3be728bb))
* **lint:** add hotpolllog AST linter and remove stale debug logs ([e40e531](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e40e53165ee4eb0862436c006ade8572a78bd352))
* **log:** runtime log level control via REST API and debug menu ([34bcaf0](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/34bcaf0d8c7298da708471828ce06a39a788d479))
* **mobile:** collapse secondary toolbar actions into ··· overflow menu ([ef342b6](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ef342b6312fb09a34d891420612c0215fcad0bd1))
* **ratelimit:** detect Claude rate limits and auto-resume sessions ([#53](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/53)) ([ff5119f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/ff5119f3d5bce43ac44779bf7daf6da4a54b609e))
* **ratelimit:** push-based output notification + repository improvements ([a0660b9](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a0660b9636c973d748d220e20b750556f32491b7))
* **rules:** close coverage gaps found in approval analytics ([68602a4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/68602a4cab7b5f857d78e49fef5a3265f5adb03e))
* **session:** add ClearConversationState RPC + expand registry test coverage ([c7e9547](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/c7e9547409c8d12e34bda6f9d4b6482bed5ad969))
* **session:** add New Project creation mode with git init ([#52](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/52)) ([e2e8964](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e2e89640ae5fef699ce4486fd4bdeb73315a30d6))
* **session:** persist review queue interaction state through restarts ([e22a4ca](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/e22a4ca4b821d7ca51847312d3a38ae8cc922c68))
* **ssq-hooks:** add claude install target and proper hook output format ([#45](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/45)) ([01d77ac](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/01d77ac93fa6481bab7c9117b6dba7e7ca0b39ed))
* **streaming:** WebSocket bridge for Watch* RPCs + global session context ([a374322](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a374322b9c7a9c618e8d9623dbc6ecf6f9268f27))
* **tmux:** priority CM sender for low-latency input forwarding ([93cfa92](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/93cfa924bced4a9442499463ed6112f4c73c3c11))
* **unfinished:** view diff modal + extract DiffRenderer as shared component ([47aa99f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/47aa99f8e94212aa2947934922e47b6fb7cb8ab7))


### Bug Fixes

* address PR review comments from Copilot ([d8aded5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d8aded55a3a98e4afd8dfdb6b27461b7e865e5ef))
* **adr-003:** eliminate all time.Sleep from test files; enforce with lint gate ([6c18a96](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6c18a965647c14796a74a5de10b8a96a48d79f50))
* **bench:** restore benchmark baselines from upstream-fanatics/main ([2aa57d7](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/2aa57d7d27398207519314c35c63e286ea70d296))
* **concurrency:** migrate mutexes to deadlock-detecting wrappers + zombie reaper refactor ([f8f012e](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f8f012eb0f4f06a8bb722c7035c293b4b862bc31))
* **detection:** fix review queue misclassifying active sessions as idle ([#54](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/54)) ([d076dbe](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d076dbed01373a2488ecc914783e4caf09f69e24))
* **install:** build ssq-hooks to ~/.local/bin during make install ([3321fc5](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3321fc5256673fa3805f2e36d81df2321638d881))
* **lint:** fix forbidigo violations in test watchdog helpers ([28645ac](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/28645aca7e0d6b18ef8ad01e14e9de933cd73932))
* **lint:** remove invalid _comment property from boundaries/dependencies rule ([3969fb4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3969fb4ac60e6556521a7d15ce9d593849acbb8f))
* **lint:** suppress gochecknoglobals on runtimeLevel atomic ([0058fad](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/0058fad85a9498e230f64b76ecb9a63411afd3e5))
* **omnibar:** session name typing no longer resets one-off creation mode ([b1398cc](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/b1398cc0f0724a9d58efd4d52a45a10d47c76aff))
* **review:** address Copilot review comments ([6464477](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/64644771da87ea07f46e1afad16715654de0d38e))
* **review:** address remaining Copilot review comments ([9d25b4f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/9d25b4fa93f96f8c74564246eb8006766e22e0e3))
* **server:** use poller cache for WatchSessions; block on PTY pause ([18cd90f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/18cd90f2bbae9d310b4b9493258b79288b81154c))
* **session:** reduce zombie accumulation via WaitDelay and HistoryLinker backoff ([46756ef](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/46756ef2a412d024381f4ceb8bf4df26e14acb44))
* **streaming:** replace polling with push-based updates for VCS and notifications ([14960de](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/14960dec217262ae8378a3795662023e983eaa24))
* **test:** prevent tmux server socket leaks across test runs ([7485594](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/7485594cd1466a96b590b687a53e6db90f0a55e7))
* tmux session creation reliability and review queue force-advance ([a3d8655](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a3d865505fb239d522896d94a40ae230ea6b850e))
* **tmux:** initialize priority channels in CM dispatch test helper ([f9da30f](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f9da30f765cf9bc202b9d6a233cb41cb2f5268de))
* **tmux:** initialize priority channels in CM dispatch test helper ([a7ae705](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a7ae705be731801ec561bc769b4bca08541e894f))
* **tmux:** non-blocking resize and fire-and-forget input to cut CM round-trips ([f0716d2](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/f0716d2133a3fe0a95a5518b051bcc8bc238e19a))
* **ui:** auto-reload once on ChunkLoadError to recover stale build cache ([3fd8ba2](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/3fd8ba29aa9106b773f04e37244a4eb9f6d503af))
* **ui:** pre-populate worktree dropdown when repo is pre-selected ([784eac4](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/784eac4caf254382caa6ce2f28334210e48cce91))
* **ui:** remove nav click handler that broke Next.js routing ([0cbeda8](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/0cbeda8f0b8b0a08e929ebd301ab2a83634bab09))
* **unfinished:** compare against remote tracking refs and add GetWorktreeDiff RPC ([dc605ad](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/dc605adcb1d208db902fd4baeba82ac62ce84205))


### Performance Improvements

* eliminate mutex contention and allocation hotspots ([#94](https://github.com/TylerStaplerAtFanatics/stapler-squad/issues/94)) ([6f3b278](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/6f3b2781f5420d425741cf390f97ad3e94d1ce65))

## [1.25.0](https://github.com/TylerStaplerAtFanatics/stapler-squad/compare/v1.24.0...v1.25.0) (2026-05-01)


### Features

* **zombie:** set PR_SET_CHILD_SUBREAPER on Linux so tmux's zombies reparent to us ([d597fd6](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/d597fd653dbbc7e24625a7e86fd2615125a3e3ef))


### Bug Fixes

* **ui:** show GitHub info in VCS tab; fix action sheet z-index ([9b95d93](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/9b95d93c8a185bcd2c058f54c7399d5dceb6efc8))
* **zombie:** filter to direct children only, add spawn registry for origin logging ([a7d8fe2](https://github.com/TylerStaplerAtFanatics/stapler-squad/commit/a7d8fe27d17d6caf1ca3c8e473fb4b106fbbb123))
