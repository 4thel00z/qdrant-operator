# Changelog

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
