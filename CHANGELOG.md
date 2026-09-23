# Changelog

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
