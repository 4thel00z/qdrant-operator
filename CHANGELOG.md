# Changelog

## [0.7.1](https://github.com/4thel00z/qdrant-operator/compare/v0.7.0...v0.7.1) (2026-11-28)


### Build

* **docker:** cross-compile instead of emulating the target platform ([e5eb21e](https://github.com/4thel00z/qdrant-operator/commit/e5eb21eb9d28167fd25408d932d5028b28d2ca6b))


### CI

* **release:** download only the binaries and the chart for the release assets ([7fa75be](https://github.com/4thel00z/qdrant-operator/commit/7fa75be5036d1813108f69821b300186b8764ace))

## [0.7.0](https://github.com/4thel00z/qdrant-operator/compare/v0.6.2...v0.7.0) (2026-11-28)


### ⚠ BREAKING CHANGES

* remove the Python operator
* **chart:** leader election replaces kopf peering

### Features

* **chart:** leader election replaces kopf peering ([2751114](https://github.com/4thel00z/qdrant-operator/commit/27511145bfc74d8aca949f7da28d37eec36ee14a))
* **chart:** settings and leader election RBAC for the Go operator ([51d9287](https://github.com/4thel00z/qdrant-operator/commit/51d92873d79b0096325a20dc7c0082da7ab12dfd))
* **cmd:** operator entry point with cobra, fang and viper settings ([c0a55b5](https://github.com/4thel00z/qdrant-operator/commit/c0a55b54f7601a4fd05f78a4c1f995c21bf7f373))
* **controller:** access key and migration reconcilers ([d66d851](https://github.com/4thel00z/qdrant-operator/commit/d66d8511fa88c5a5c32b5b5c06b8298b0ccce61f))
* **controller:** backup, restore and schedule reconcilers ([a15aa61](https://github.com/4thel00z/qdrant-operator/commit/a15aa6160eb8119665ef79e489fceb4818af26eb))
* **controller:** QdrantCluster reconciler ([936334f](https://github.com/4thel00z/qdrant-operator/commit/936334fee917f31155f27a607a1155a8513b8809))
* **controller:** record phase changes as Kubernetes events ([d1a6617](https://github.com/4thel00z/qdrant-operator/commit/d1a6617d0f4241f2e6ef77c4a2f4ea2ab07e03c1))
* **controller:** register backup, restore and schedule reconcilers ([3ac76f7](https://github.com/4thel00z/qdrant-operator/commit/3ac76f7cd6cd621490e64f3cc26ff6cbe3b75be7))
* **controller:** register the cluster, access key and migration reconcilers ([d039d93](https://github.com/4thel00z/qdrant-operator/commit/d039d93d0d474a017db54dd3ddaa76a01801d534))
* **controller:** shared reconciler pieces and the QdrantCollection controller ([ef8a1b1](https://github.com/4thel00z/qdrant-operator/commit/ef8a1b1f51f0642b2c8c1539037d189324e8c1ae))
* **cron:** port croniter schedules to gronx ([85d8ece](https://github.com/4thel00z/qdrant-operator/commit/85d8ecea522964d60824503075a81f0f2dbbcb32))
* **domain:** backups, retention, schedules and restores with Python golden tests ([da29ed3](https://github.com/4thel00z/qdrant-operator/commit/da29ed3e5363fa5f6c0d375ec48d439ffe63c395))
* **domain:** port access key claims and migration rules to Go ([df66ed8](https://github.com/4thel00z/qdrant-operator/commit/df66ed828492fffaab323baeb3bf2cabcb3b6785))
* **domain:** port cluster runtime state, rollout partitions and volume plans ([9c57fae](https://github.com/4thel00z/qdrant-operator/commit/9c57fae4757dc40e5e6ddc14e1b12fd0a7b4bd7a))
* **domain:** port peer and shard planning to Go ([3f244ef](https://github.com/4thel00z/qdrant-operator/commit/3f244efbece9fd4766a20d0fafa01bd1e5b01cfb))
* **domain:** QdrantCluster spec and Helm values with Python golden tests ([1fd2683](https://github.com/4thel00z/qdrant-operator/commit/1fd2683bbff69d6d7450f7d7abae7cc8b1177785))
* **domain:** QdrantCollection spec, diffing, plans and status with Python golden tests ([af46714](https://github.com/4thel00z/qdrant-operator/commit/af467147788297b93793066df921c23d4c11de17))
* **domain:** shared conditions, resource refs, time helpers and values digest ([3530780](https://github.com/4thel00z/qdrant-operator/commit/353078062b2ab1c91116480a6ba511fa9d318557))
* **helm:** in-process Helm v4 adapter with partition post-renderer ([5597c99](https://github.com/4thel00z/qdrant-operator/commit/5597c99a5c2eddb2194c0074c0096f18f4433694))
* **jwt:** port the HS256 token signer to golang-jwt ([6b948fb](https://github.com/4thel00z/qdrant-operator/commit/6b948fb89f80e1a891ce47280c606423675fa10e))
* **kubernetes:** implement ports.Kubernetes on controller-runtime ([92fef76](https://github.com/4thel00z/qdrant-operator/commit/92fef76b59c42387e025f9c7cc99831cb0a5200f))
* **ports:** Kubernetes port and in-memory fake ([614bbb2](https://github.com/4thel00z/qdrant-operator/commit/614bbb2e15d575bd35f2f08083d348ecd111d990))
* **qdrant:** add the Qdrant REST adapter, port and in-memory fake ([590f51d](https://github.com/4thel00z/qdrant-operator/commit/590f51d9773e0cb621022e3731edcfe5a1440ffb))
* **s3:** port the S3 storage adapter to aws-sdk-go-v2 ([cfc534d](https://github.com/4thel00z/qdrant-operator/commit/cfc534d819068e9471908124082ad35af3b512c3))
* **usecases:** backup, volume snapshot backup, restore and schedule use cases ([fdae882](https://github.com/4thel00z/qdrant-operator/commit/fdae88212e9b9a6952115a698e3e9db7dcad7f49))
* **usecases:** port collection, access key and migration use cases ([621192e](https://github.com/4thel00z/qdrant-operator/commit/621192e184b345c79a022d38df37c823cc2b1708))
* **usecases:** port the QdrantCluster use cases ([895c04a](https://github.com/4thel00z/qdrant-operator/commit/895c04a0eb873cbdcfc4553b8240e157de13a58a))
* **usecases:** ResolveCluster, credentials and retryable errors ([af12679](https://github.com/4thel00z/qdrant-operator/commit/af12679f626ef48f5cb1f1bf9c5bf9e771329268))


### Bug Fixes

* **app:** release the leader Lease on shutdown ([75e6696](https://github.com/4thel00z/qdrant-operator/commit/75e66969edd4d351267df9dfc3fd7630cae8bb2d))
* **chart:** let helm v4 watch release resources during uninstall ([98c60b3](https://github.com/4thel00z/qdrant-operator/commit/98c60b3de3279fccbe05b854fe1f3da17f2f3bbb))
* **usecases:** keep integers exact in every status patch body ([9c36be4](https://github.com/4thel00z/qdrant-operator/commit/9c36be49debb9483d535f4528a025d359530b82f))


### Documentation

* **claude:** Go commands, layout and conventions ([8ede6bc](https://github.com/4thel00z/qdrant-operator/commit/8ede6bcd02b905c62d7ad5223abb73424bf4abfb))
* describe the Go operator ([ac639aa](https://github.com/4thel00z/qdrant-operator/commit/ac639aa6d9a97e7b20a33da7e8ced351c6173f1c))


### Build

* **api:** generate CRDs from Go types in api/v1alpha1 ([4a86d32](https://github.com/4thel00z/qdrant-operator/commit/4a86d32c5b33bb28901192e6c20c5cb72a64396f))
* remove the Python operator ([26db8aa](https://github.com/4thel00z/qdrant-operator/commit/26db8aab5869c35777cafb30b9298787981e004c))

## [0.6.2](https://github.com/4thel00z/qdrant-operator/compare/v0.6.1...v0.6.2) (2026-11-23)


### Documentation

* add 0.6.x field results and the Cinder force-create snapshot class ([#42](https://github.com/4thel00z/qdrant-operator/issues/42)) ([abda85d](https://github.com/4thel00z/qdrant-operator/commit/abda85d0cb2c97c4b949afa39a643a6eaf7d9015))

## [0.6.1](https://github.com/4thel00z/qdrant-operator/compare/v0.6.0...v0.6.1) (2026-11-23)


### Bug Fixes

* **cluster:** re-apply when a failed spec is reverted to the last applied one ([#39](https://github.com/4thel00z/qdrant-operator/issues/39)) ([d4dfb15](https://github.com/4thel00z/qdrant-operator/commit/d4dfb154027fad44a85a4a0d2cd1faacad725322))
* **restore:** provision restored volumes in their snapshot's zone ([#40](https://github.com/4thel00z/qdrant-operator/issues/40)) ([d301d1d](https://github.com/4thel00z/qdrant-operator/commit/d301d1d232fc261f2d2d56e9f83b8f35554b87d1))

## [0.6.0](https://github.com/4thel00z/qdrant-operator/compare/v0.5.0...v0.6.0) (2026-11-23)


### Features

* **backup:** CSI VolumeSnapshot backup method ([6749f38](https://github.com/4thel00z/qdrant-operator/commit/6749f384c04cb45141928480dfc3934016932bcc))
* **backup:** CSI VolumeSnapshot backups, retention and restore into a new cluster ([1f77129](https://github.com/4thel00z/qdrant-operator/commit/1f771295a665b988b964cdea00aed28c801cec9c))
* **chart:** least-privilege RBAC and opt-in watchNamespaces ([1986c81](https://github.com/4thel00z/qdrant-operator/commit/1986c8189ad2e57b632f79c87e725d764f3abc88))
* **chart:** least-privilege RBAC and opt-in watchNamespaces ([47c088d](https://github.com/4thel00z/qdrant-operator/commit/47c088dc5e02c2678c07202f68046de0ef2c917d))
* **chart:** run several operator replicas with kopf peering ([add5110](https://github.com/4thel00z/qdrant-operator/commit/add51102e054f7a4e1bc969ab51eaef23587a9a9))
* **chart:** run several operator replicas with kopf peering ([af07ef3](https://github.com/4thel00z/qdrant-operator/commit/af07ef3cd916545a399a62f2911fcac924024fec))
* **cluster:** make the TLS Secret's CA key configurable ([c572a3b](https://github.com/4thel00z/qdrant-operator/commit/c572a3bab6ec1377e2effa07b300c5f2ce979156))
* **crds:** add volumeSnapshot backup method with CEL validation ([3c159e0](https://github.com/4thel00z/qdrant-operator/commit/3c159e0b4f31d23197b6e8e6b15ae48a472f97e4))
* **migration:** add opt-in sync pass for updates and deletes ([d0a518d](https://github.com/4thel00z/qdrant-operator/commit/d0a518d84496df5fb5c2c8593b9f8482ee7a3882))
* **migration:** add opt-in sync pass for updates and deletes made during a copy ([06c299b](https://github.com/4thel00z/qdrant-operator/commit/06c299b72eb1313697b13ebe2ff6d49c489dfc52))


### Bug Fixes

* **cluster:** keep HelmApplyFailed visible until a later apply succeeds ([2683c9d](https://github.com/4thel00z/qdrant-operator/commit/2683c9d118b72941711e20a76ca0891a0b823683))
* **cluster:** point Qdrant at the TLS Secret's CA for p2p TLS ([5268d0a](https://github.com/4thel00z/qdrant-operator/commit/5268d0aac449a70348c670e5a285081b32d88834))


### Documentation

* **backup:** document volume snapshot backups and restore constraints ([b51c835](https://github.com/4thel00z/qdrant-operator/commit/b51c835279eed41f15ccadaa95a8bc3837a0eda8))

## [0.5.0](https://github.com/4thel00z/qdrant-operator/compare/v0.4.0...v0.5.0) (2026-11-23)


### Features

* **cluster:** roll pods only after every shard replica is Active ([3f4dcc3](https://github.com/4thel00z/qdrant-operator/commit/3f4dcc325dabb4adbe3d1a453a88dd5a0e07f9e9))
* **cluster:** roll pods only after every shard replica is Active ([50a6e24](https://github.com/4thel00z/qdrant-operator/commit/50a6e24a65e26a848770e8131be13002b9ec6c75))


### Bug Fixes

* **ci:** uv.lock for 0.4.0 and kind integration tests ([eec1ce9](https://github.com/4thel00z/qdrant-operator/commit/eec1ce9fad82ebbaa9a4b6593f4785facc285d99))


### Documentation

* **readme:** add field test results with figures ([0b8e6a1](https://github.com/4thel00z/qdrant-operator/commit/0b8e6a1c8e0aec270a264dc073cf5477af0ca6f7))
* **readme:** add field test results with figures ([9c51110](https://github.com/4thel00z/qdrant-operator/commit/9c5111041c2aaa36581b39af05d68b901900a83d))


### Build

* refresh uv.lock for 0.4.0 and bump it on release ([b9b7089](https://github.com/4thel00z/qdrant-operator/commit/b9b70894d3255db69409bb861434d97183d90513))


### CI

* **integration:** run the adapter against the helm version the image ships ([db3cf9f](https://github.com/4thel00z/qdrant-operator/commit/db3cf9fb2308279a90791c4643c4efd15337ab70))
* **release:** publish an existing tag to PyPI on demand ([328579d](https://github.com/4thel00z/qdrant-operator/commit/328579d719b3a5854b1be83d8a4e9c9d2bec8ddf))
* **release:** publish an existing tag to PyPI on demand ([75584c5](https://github.com/4thel00z/qdrant-operator/commit/75584c5e64bbc1c0103e55a2112233ede4909d1b))

## [0.4.0](https://github.com/4thel00z/qdrant-operator/compare/v0.3.2...v0.4.0) (2026-11-21)


### Features

* lifecycle and data-safety parity with Qdrant Hybrid Cloud ([4f30f93](https://github.com/4thel00z/qdrant-operator/commit/4f30f9366ac391fd766cf3c69d66776085b8a37a))
* lifecycle and data-safety parity with Qdrant Hybrid Cloud ([0935991](https://github.com/4thel00z/qdrant-operator/commit/0935991c9ce43ee03a40303fc5bd21654dcd8ed4))


### Bug Fixes

* **handlers:** keep level-triggered timers off the create handler ([0bb910f](https://github.com/4thel00z/qdrant-operator/commit/0bb910feb3e55b3f9cdb6d02f16973df7a1d8dbc))
* **kubernetes:** keep merge-patch content type under kubernetes-asyncio 36 stubs ([3fd4a36](https://github.com/4thel00z/qdrant-operator/commit/3fd4a362fea5a552d82d1620e3d17f2d79ade698))


### Build

* **deps:** bump the python group across 1 directory with 7 updates ([51343df](https://github.com/4thel00z/qdrant-operator/commit/51343df96512180078ad38e896031f824c7a8b6d))
* **docker:** python 3.14-slim base image ([99b185b](https://github.com/4thel00z/qdrant-operator/commit/99b185b77da834a5327175cdcaa183c2f9086cae))


### CI

* download the dist and chart artifacts by name ([034ec75](https://github.com/4thel00z/qdrant-operator/commit/034ec757ad9487cb725a52cd635464ac180c21ef))
* end-to-end job against the operator deployed by its chart ([bebbb74](https://github.com/4thel00z/qdrant-operator/commit/bebbb74109933b0590504b9f70be9710719c3d53))
* end-to-end job against the operator deployed by its chart ([3044acd](https://github.com/4thel00z/qdrant-operator/commit/3044acd7a2a15d09496229613b3ec956f91e1db5))

## [0.3.2](https://github.com/4thel00z/qdrant-operator/compare/v0.3.1...v0.3.2) (2026-09-24)


### Bug Fixes

* **s3,helm:** bound multipart buffering, recover pending releases, 512Mi default ([7d9716d](https://github.com/4thel00z/qdrant-operator/commit/7d9716d9ac476535087626e6bb069d0775257eca))


### CI

* download-artifact v8 to match upload-artifact v7 ([ef2fe6c](https://github.com/4thel00z/qdrant-operator/commit/ef2fe6c7f242aff85195bc95944bc71f85852dc0))

## [0.3.1](https://github.com/4thel00z/qdrant-operator/compare/v0.3.0...v0.3.1) (2026-09-24)


### Bug Fixes

* **chart:** grant statefulsets/status so the cluster observer can read readiness ([cb6c064](https://github.com/4thel00z/qdrant-operator/commit/cb6c06481f79f22af056015f318faf5238630b11))
* **kubernetes:** status patches as merge-patch ([e3b14b0](https://github.com/4thel00z/qdrant-operator/commit/e3b14b058cd76c756298d0bf72858ab3439ec7f8))
* **qdrant:** name the unreachable node in transport errors ([ce1cc11](https://github.com/4thel00z/qdrant-operator/commit/ce1cc1177f9eb9ba7b3953cde95a4c50c1f75fc7))

## [0.3.0](https://github.com/4thel00z/qdrant-operator/compare/v0.2.1...v0.3.0) (2026-09-24)


### Features

* **accesskey:** claims, durations and renewal rules in the domain ([6aa5d6f](https://github.com/4thel00z/qdrant-operator/commit/6aa5d6f55de5fd0a9a73e87eb69c744979410e79))
* **accesskey:** issue and renew tokens on a timer ([984a584](https://github.com/4thel00z/qdrant-operator/commit/984a58437f5b85728e182433490ae1a28859c1d7))
* **accesskey:** QdrantAccessKey CRD and apiKey.jwtRbac on QdrantCluster ([d39533c](https://github.com/4thel00z/qdrant-operator/commit/d39533c2d189419450ffb7e501676f23b4928324))
* **accesskey:** TokenPort with a PyJWT adapter and owned Secret writes ([332d656](https://github.com/4thel00z/qdrant-operator/commit/332d6563f0ecfc9cda37862936296eb015df52d0))
* **cluster:** podAnnotations and podLabels on QdrantCluster ([2075aa9](https://github.com/4thel00z/qdrant-operator/commit/2075aa9f5821d05a327dbed2efef1b3b45199623))
* **collection:** collection, alias and payload index calls on the Qdrant port ([af18e21](https://github.com/4thel00z/qdrant-operator/commit/af18e21ce2e9d969305065eb38d9181e150bb53b))
* **collection:** domain model and Qdrant body derivation ([9710139](https://github.com/4thel00z/qdrant-operator/commit/97101396e069905514f523539805cbcc70735eb6))
* **collection:** level-triggered reconcile and deletion policy ([f411023](https://github.com/4thel00z/qdrant-operator/commit/f411023bb90f9f9da423d5ee8419035786a30505))
* **collection:** memory tiers, metadata, payload storage and shard keys ([3e06158](https://github.com/4thel00z/qdrant-operator/commit/3e0615842cb609db004ec0614e6af5ba96e3f059))
* **collection:** QdrantCollection CRD ([7114157](https://github.com/4thel00z/qdrant-operator/commit/71141571a14f37113baea2617dadbbbe3b7d34c2))
* **crds:** CEL admission rules on the existing resources ([716e439](https://github.com/4thel00z/qdrant-operator/commit/716e439e47a30c271e6098afe8c9f2e40ba454ac))
* **migration:** count, scroll and upsert on the Qdrant port ([c85753b](https://github.com/4thel00z/qdrant-operator/commit/c85753b2b512f01a22300679651b955a8e710405))
* **migration:** ExecuteMigration use case and handler ([fd63647](https://github.com/4thel00z/qdrant-operator/commit/fd63647d6dc0fef5ca6fcf7e74f2f106b8d1953e))
* **migration:** QdrantMigration CRD ([8001220](https://github.com/4thel00z/qdrant-operator/commit/8001220f8666b2e485f0876b8ef1f5f83350b4ec))
* **migration:** resume from status, shard-key preservation and routing ([1e58a7b](https://github.com/4thel00z/qdrant-operator/commit/1e58a7bfae25c06c87c303660bbde0a431129fad))
* **migration:** source, target overrides and config-to-body translation ([388a76c](https://github.com/4thel00z/qdrant-operator/commit/388a76c6d52098f36e7ea1f98b5761459643a864))


### Bug Fixes

* **accesskey:** never overwrite a Secret this resource does not own ([7391820](https://github.com/4thel00z/qdrant-operator/commit/7391820c85d592c32a535d7d4e511a8abd06a655))
* **cluster:** render service.jwt_rbac only when enabled ([edcdae9](https://github.com/4thel00z/qdrant-operator/commit/edcdae9b3dc23ac00cbb0310d599a927cea75cab))
* **collection:** PATCH only the blocks the live config misses ([5924b09](https://github.com/4thel00z/qdrant-operator/commit/5924b090b5020138138b79adf95b93e9f978fcfb))
* **collection:** pinned is not a dense-vector memory tier; pin version notes to 1.19 ([d25800e](https://github.com/4thel00z/qdrant-operator/commit/d25800ee0ec09a8298597db210dd2319258f6fe8))
* **migration:** no shard keys on auto-sharded targets, e2e for custom sharding ([83a5051](https://github.com/4thel00z/qdrant-operator/commit/83a505172429fd2314e8e93b4eb4a50eea401f83))
* **types:** AsyncGenerator return types on asynccontextmanager functions ([3a17d9a](https://github.com/4thel00z/qdrant-operator/commit/3a17d9ad0e6116d2a137e47e4fbc09a8fce601b8))


### Documentation

* **book:** access keys and migrations ([89283ed](https://github.com/4thel00z/qdrant-operator/commit/89283ed101c43b3f71f93e9d2ce56eb369fa40ab))
* **book:** correct suspend, restart and snapshotPersistence behavior ([223a752](https://github.com/4thel00z/qdrant-operator/commit/223a75281b5792158bf5422e87cacbffcac8dcd1))
* CRD upgrade step and the custom-sharding migration limitation ([bc91aba](https://github.com/4thel00z/qdrant-operator/commit/bc91aba99312a43e3208300e699fdb96b9c6af36))
* mdBook site on GitHub Pages ([d998a88](https://github.com/4thel00z/qdrant-operator/commit/d998a889ba71d9a1c2a089afcf8f995418f8ea77))
* QdrantCollection, QdrantAccessKey and QdrantMigration ([19858f7](https://github.com/4thel00z/qdrant-operator/commit/19858f7927805f5db42ba906b9e347cc0822d60c))
* shard keys, metadata and resumable migrations ([bf312b4](https://github.com/4thel00z/qdrant-operator/commit/bf312b4507fd685006ec2238c64e8a1c83508dcf))


### Build

* **deps:** bump actions/upload-artifact from 4 to 7 ([a98e750](https://github.com/4thel00z/qdrant-operator/commit/a98e750b16d9b53be94e30ad82520f8fd0ee3158))
* **deps:** bump astral-sh/setup-uv from 6 to 7 ([a6fd977](https://github.com/4thel00z/qdrant-operator/commit/a6fd977b6340aa3bb5409e36d98b1bea7fef4972))
* **deps:** bump azure/setup-helm from 4 to 5 ([67ea9ad](https://github.com/4thel00z/qdrant-operator/commit/67ea9ad4c32612e08fdca4b18f5fe26f5053a0a5))
* **deps:** bump docker/login-action from 3 to 4 ([273892e](https://github.com/4thel00z/qdrant-operator/commit/273892edf3acd8a084b8b84d4c8a1824cf2ad7a3))

## [0.2.1](https://github.com/4thel00z/qdrant-operator/compare/v0.2.0...v0.2.1) (2026-09-23)


### Bug Fixes

* **readme:** absolute logo URL so PyPI renders it ([c587482](https://github.com/4thel00z/qdrant-operator/commit/c587482146455eed2ca4e3ed6f530b1fd71cd14b))

## [0.2.0](https://github.com/4thel00z/qdrant-operator/compare/v0.1.0...v0.2.0) (2026-09-23)


### Features

* add container and main entry point ([cd5f1c7](https://github.com/4thel00z/qdrant-operator/commit/cd5f1c726eba197dc919e77c8ea09485abe44cb4))
* add Helm chart for operator deployment ([810267e](https://github.com/4thel00z/qdrant-operator/commit/810267e4ff1cf422a833044d9a3e02f4bd1b3b14))
* add integration tests and fix image tag ([ee902af](https://github.com/4thel00z/qdrant-operator/commit/ee902af4d374020bb2542664db891645ffac0616))
* add integration tests for cluster lifecycle ([31d192e](https://github.com/4thel00z/qdrant-operator/commit/31d192e96c144b0bfe9be8bcacab5c08cd7a32a1))
* add kopf handlers for all CRDs ([7d6d918](https://github.com/4thel00z/qdrant-operator/commit/7d6d918861810edca8d36ef342e1400902cec5d5))
* add ports.py with Protocol interfaces ([8859ec0](https://github.com/4thel00z/qdrant-operator/commit/8859ec0a201a7ff38667513556c4473b0beabc5e))
* add S3 and Kubernetes adapters ([75a3188](https://github.com/4thel00z/qdrant-operator/commit/75a3188b35ff74fe050d306cabf5188b1c7d44fa))
* add usecases.py with application logic ([b7e07eb](https://github.com/4thel00z/qdrant-operator/commit/b7e07eb858f83c58528c2d3153105273fd5f8f51))
* **chart:** liveness probe, helm cache env, trimmed RBAC, script entrypoint ([c565e29](https://github.com/4thel00z/qdrant-operator/commit/c565e298f9eca721436f1d3802e622158f1aeb77))
* complete operator implementation ([e579bd7](https://github.com/4thel00z/qdrant-operator/commit/e579bd70db158b17f73fbe6941f7ecfa6253ab71))
* **crds:** per-node snapshot status, expiresAt, lastScheduleTime, no_sync priority ([f37b6a9](https://github.com/4thel00z/qdrant-operator/commit/f37b6a972bf0d4633a3e98e74c9fadd2e77dbb31))
* **handlers:** write status via patch.status, add resume/timers/probe ([c8719b8](https://github.com/4thel00z/qdrant-operator/commit/c8719b84ebf74bb123c21b8e8043c245c99a8ad8))
* **helm:** stateless upgrade --install with values over stdin ([5529c36](https://github.com/4thel00z/qdrant-operator/commit/5529c36a5bb099009eae4169b55a31492cc669be))
* **kubernetes:** load config once, close clients, map 404s ([517295a](https://github.com/4thel00z/qdrant-operator/commit/517295a86aa4368ddfb845f4a349ac073ee9721d))
* **qdrant:** per-node client, streamed snapshots, native recover endpoint ([6e4ef2b](https://github.com/4thel00z/qdrant-operator/commit/6e4ef2bf17596b26f6e00339c116005cc72f7e25))
* **s3:** streaming multipart upload, presigned URLs and prefix deletion ([68cda25](https://github.com/4thel00z/qdrant-operator/commit/68cda2517dc8d0ec162e548790d446bd75740424))
* **usecases:** multi-node backup, manifest-driven restore, CronJob schedules ([bad9e8d](https://github.com/4thel00z/qdrant-operator/commit/bad9e8dfc247ffda10c2cd440fdef5e56f3da74a))


### Bug Fixes

* **docker:** ship README/LICENSE for the build backend and use a dedicated user ([9943b5e](https://github.com/4thel00z/qdrant-operator/commit/9943b5ef857c37289223dc60a2d3d1d29644860c))
* use UTC for all datetime calls ([f1af3d6](https://github.com/4thel00z/qdrant-operator/commit/f1af3d6cfd9963866bc2ec29b57d0579bb81f8cc))


### Refactoring

* clean up QdrantAdapter and QdrantPort ([ff86bdd](https://github.com/4thel00z/qdrant-operator/commit/ff86bdd4501cc7cb9c39b090773361219ee667f6))
* **domain:** rewrite domain model with from_dict/to_dict and pure rules ([efe34af](https://github.com/4thel00z/qdrant-operator/commit/efe34afa91eb26171c1b8abc6cee5e5576f5509e))
* **ports:** redefine ports around nodes, streams and resource refs ([97f7cc9](https://github.com/4thel00z/qdrant-operator/commit/97f7cc9e3312383beae41234d0a37dab8e69b76d))
* update usecases to use new QdrantPort interface ([56878ff](https://github.com/4thel00z/qdrant-operator/commit/56878ff1f358bff733b604e1d2baa2e2ccfa61bc))


### Documentation

* add docs with usage/installation ([b92829c](https://github.com/4thel00z/qdrant-operator/commit/b92829cd3241d56ed43067cc576b91b36d36ef56))
* describe the rewritten architecture, bucket layout and test strategy ([d0d9b6e](https://github.com/4thel00z/qdrant-operator/commit/d0d9b6e85fbaa37fcd180028984ba1e8c9a23399))


### Build

* replace structlog with loguru and add packaging metadata ([9be4243](https://github.com/4thel00z/qdrant-operator/commit/9be42432dd87fdc9e548aca6b4de5555951198c2))


### CI

* drive releases with release-please ([9d42257](https://github.com/4thel00z/qdrant-operator/commit/9d42257e80c70a298fbb8e708d30aa594da09384))
* GitHub Actions CI and tag-driven release to PyPI, ghcr.io and OCI chart ([62034e5](https://github.com/4thel00z/qdrant-operator/commit/62034e5380a9a1e83e8e888d8f0e79e6c2e1bd3f))
* let feat commits bump the minor version and refresh uv.lock for real ([4b45924](https://github.com/4thel00z/qdrant-operator/commit/4b45924c452daf4c190c27f49eb6a3659e9fd5af))
* publish from the release-please run so no PAT is needed ([0b777fb](https://github.com/4thel00z/qdrant-operator/commit/0b777fb8aadf78db7bada4f3428b06fd7dcf9646))
* uv rejects an empty UV_FROZEN, use 0 ([e876166](https://github.com/4thel00z/qdrant-operator/commit/e876166d52b8bbc7083a7acba414a6f3a608fc39))
