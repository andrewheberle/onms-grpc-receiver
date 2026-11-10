# Changelog

## [0.14.1](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.14.0...v0.14.1) (2026-11-10)


### Bug Fixes

* **deps:** update go toolchain directive to v1.27.4 ([#187](https://github.com/andrewheberle/onms-grpc-receiver/issues/187)) ([b8658c0](https://github.com/andrewheberle/onms-grpc-receiver/commit/b8658c0d56350406b058b5fb4aac5acac07eca3a))
* **deps:** update module github.com/andrewheberle/simplecommand/koanfcommand to v0.1.3 ([#184](https://github.com/andrewheberle/onms-grpc-receiver/issues/184)) ([79f52ab](https://github.com/andrewheberle/onms-grpc-receiver/commit/79f52abb2ced83a8cc3eb26937f1c79337fb71e3))
* **deps:** update module github.com/prometheus/alertmanager to v0.35.0 ([#185](https://github.com/andrewheberle/onms-grpc-receiver/issues/185)) ([8432bcd](https://github.com/andrewheberle/onms-grpc-receiver/commit/8432bcd1b1a649be658aac2efdbfdfe03f0755e1))

## [0.14.0](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.13.2...v0.14.0) (2026-11-06)


### Features

* add a healthcheck command and a Docker HEALTHCHECK ([#177](https://github.com/andrewheberle/onms-grpc-receiver/issues/177)) ([2a87cb9](https://github.com/andrewheberle/onms-grpc-receiver/commit/2a87cb915eaff321120fbc20a21dd9759a3b90e5))
* **server:** close gRPC connections that stop answering keepalive pings ([#173](https://github.com/andrewheberle/onms-grpc-receiver/issues/173)) ([019266d](https://github.com/andrewheberle/onms-grpc-receiver/commit/019266df1255f0106430535b37511ce6ab0019f8))
* **server:** report readiness over gRPC health and /-/ready ([#172](https://github.com/andrewheberle/onms-grpc-receiver/issues/172)) ([aa794bb](https://github.com/andrewheberle/onms-grpc-receiver/commit/aa794bb0b8dc715a6c5b93358a1a4234c5476c51))
* **server:** retry and chunk alert delivery ([#167](https://github.com/andrewheberle/onms-grpc-receiver/issues/167)) ([0c8e549](https://github.com/andrewheberle/onms-grpc-receiver/commit/0c8e54922a497c3ec8009347381d88b77c2dab1a))


### Bug Fixes

* **server:** keep credentials and query strings out of metrics, logs and errors ([#170](https://github.com/andrewheberle/onms-grpc-receiver/issues/170)) ([03eb190](https://github.com/andrewheberle/onms-grpc-receiver/commit/03eb1900777b8ccb60c97cf4830fab18ea77936f))
* **server:** refuse credentials in --map.url ([#171](https://github.com/andrewheberle/onms-grpc-receiver/issues/171)) ([6f077be](https://github.com/andrewheberle/onms-grpc-receiver/commit/6f077bea6bc0e80bdb7175ec0e16df4190a79890))
* **server:** validate configuration at startup ([#169](https://github.com/andrewheberle/onms-grpc-receiver/issues/169)) ([6f07a05](https://github.com/andrewheberle/onms-grpc-receiver/commit/6f07a05618e9fe21613043b2e81829b283873f90))

## [0.13.2](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.13.1...v0.13.2) (2026-11-02)


### Bug Fixes

* **deps:** update cgr.dev/chainguard/static:latest docker digest to fc42af3 ([#164](https://github.com/andrewheberle/onms-grpc-receiver/issues/164)) ([458612d](https://github.com/andrewheberle/onms-grpc-receiver/commit/458612d0ce47f638e872ccafefe01e2b0d6bac71))

## [0.13.1](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.13.0...v0.13.1) (2026-10-29)


### Bug Fixes

* **deps:** update module github.com/prometheus/alertmanager to v0.34.2 ([#162](https://github.com/andrewheberle/onms-grpc-receiver/issues/162)) ([49c2e9f](https://github.com/andrewheberle/onms-grpc-receiver/commit/49c2e9fb0d96730ab8d03d22414614750eeaea50))
* **deps:** update module google.golang.org/grpc to v1.85.0 ([#160](https://github.com/andrewheberle/onms-grpc-receiver/issues/160)) ([b440077](https://github.com/andrewheberle/onms-grpc-receiver/commit/b440077ccf142c9d267ecd386f72f437cf1a7719))

## [0.13.0](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.12.1...v0.13.0) (2026-10-26)


### Features

* add severity map to set alert severities for OpenNMS severities ([#158](https://github.com/andrewheberle/onms-grpc-receiver/issues/158)) ([cbf5df6](https://github.com/andrewheberle/onms-grpc-receiver/commit/cbf5df619535a4e4ac0c824a9d33f86e59bd3021))


### Bug Fixes

* **deps:** update cgr.dev/chainguard/static:latest docker digest to c12f010 ([#155](https://github.com/andrewheberle/onms-grpc-receiver/issues/155)) ([08bef6d](https://github.com/andrewheberle/onms-grpc-receiver/commit/08bef6d617d9d6b611aa5b559aa6df6b6143e02e))
* **server:** resolve firing alert when alarm severity changes or is cleared ([#159](https://github.com/andrewheberle/onms-grpc-receiver/issues/159)) ([dc05dd8](https://github.com/andrewheberle/onms-grpc-receiver/commit/dc05dd84e3ab61547ef8d559567032e281a32262))

## [0.12.1](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.12.0...v0.12.1) (2026-10-23)


### Bug Fixes

* **release:** enable immutable releases ([#152](https://github.com/andrewheberle/onms-grpc-receiver/issues/152)) ([f784e83](https://github.com/andrewheberle/onms-grpc-receiver/commit/f784e8377c6ea4abbe9b424bbcb1b249b2beeb0e))

## [0.12.0](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.11.0...v0.12.0) (2026-10-22)


### Features

* **server:** optional hold for alarm alerts ([#149](https://github.com/andrewheberle/onms-grpc-receiver/issues/149)) ([e831c3e](https://github.com/andrewheberle/onms-grpc-receiver/commit/e831c3e29e188ac833a718e356df7c802c64bb3c))


### Bug Fixes

* **deps:** update module github.com/prometheus/alertmanager to v0.34.1 ([#102](https://github.com/andrewheberle/onms-grpc-receiver/issues/102)) ([6709653](https://github.com/andrewheberle/onms-grpc-receiver/commit/67096530b75349e2454bf36cd36026ce1472d20d))
* **docker:** switch base image to Chainguard static ([#147](https://github.com/andrewheberle/onms-grpc-receiver/issues/147)) ([3479328](https://github.com/andrewheberle/onms-grpc-receiver/commit/34793283f12ecc628df06a618a974ca7f8bd0c70))
* **server:** derive cleared alarm window from resolve timeout ([#148](https://github.com/andrewheberle/onms-grpc-receiver/issues/148)) ([cf9a52a](https://github.com/andrewheberle/onms-grpc-receiver/commit/cf9a52a87fe3da0d03cf2f6f578bb9fdda053d42))

## [0.11.0](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.10.0...v0.11.0) (2026-10-21)


### ⚠ BREAKING CHANGES

* **server:** ServiceSyncServer.Start is removed. Use ServiceSyncServer.Serve, which also creates and runs the gRPC server on the given listener, instead of registering the ServiceSyncServer with a gRPC server and calling Start.

### Features

* **server:** add runbook URL template for alerts ([#135](https://github.com/andrewheberle/onms-grpc-receiver/issues/135)) ([9925530](https://github.com/andrewheberle/onms-grpc-receiver/commit/9925530ec2e41270a859c43ee639a9670345c7bb))
* **server:** expose time of last alarm snapshot per instance ([#133](https://github.com/andrewheberle/onms-grpc-receiver/issues/133)) ([931bc3f](https://github.com/andrewheberle/onms-grpc-receiver/commit/931bc3fa18651cdfa82b9c89fee9b90f9ecb64fa))


### Bug Fixes

* **server:** do not fail when stopped before serving has started ([#143](https://github.com/andrewheberle/onms-grpc-receiver/issues/143)) ([91c813e](https://github.com/andrewheberle/onms-grpc-receiver/commit/91c813ed5c95f5efb559eaf92de8b6e3ebda2e5c))
* **server:** reject a resolve timeout of zero or less ([#137](https://github.com/andrewheberle/onms-grpc-receiver/issues/137)) ([e2396d5](https://github.com/andrewheberle/onms-grpc-receiver/commit/e2396d5d16ba31246e3a2b8a8b78851b6c8f6295))


### Code Refactoring

* **server:** replace Start with Serve to own the shutdown order ([#141](https://github.com/andrewheberle/onms-grpc-receiver/issues/141)) ([1334f72](https://github.com/andrewheberle/onms-grpc-receiver/commit/1334f72557e78c2df93c08c035b1943d7360f138))

## [0.10.0](https://github.com/andrewheberle/onms-grpc-receiver/compare/v0.9.10...v0.10.0) (2026-10-21)


### ⚠ BREAKING CHANGES

* **server:** ServiceSyncServer.Start now takes a context, cancelling it stops and drains the server, and Shutdown is removed. WithBatchMaxSize and WithBatchMaxWait are renamed WithFlushThreshold and WithFlushInterval.

### Features

* allow labels and annotations to be added to all alerts ([86f99c2](https://github.com/andrewheberle/onms-grpc-receiver/commit/86f99c2c8a46774bd0dfac64cf14a70e7de69f41))
* trim labels and add annotations ([2de20c8](https://github.com/andrewheberle/onms-grpc-receiver/commit/2de20c8267b4fdcb6df8a2293beef3bf0ebd5a7a))


### Bug Fixes

* **cmd:** shut down gracefully on SIGINT and SIGTERM ([#131](https://github.com/andrewheberle/onms-grpc-receiver/issues/131)) ([4f0793a](https://github.com/andrewheberle/onms-grpc-receiver/commit/4f0793a49905f51b670838f90f87b1e6381b7205))
* **deps:** update module google.golang.org/grpc to v1.84.0 ([#120](https://github.com/andrewheberle/onms-grpc-receiver/issues/120)) ([1e3dd4e](https://github.com/andrewheberle/onms-grpc-receiver/commit/1e3dd4eea76bd8b4715d1fc51bc9790cdb4e115e))
* fix firing alarms from snapshots being dropped ([bb5f90f](https://github.com/andrewheberle/onms-grpc-receiver/commit/bb5f90fc5d7c2086ea0e785ef0227604f484a8ad))
* **server:** do not modify the alertmanager URL list passed in ([ec66851](https://github.com/andrewheberle/onms-grpc-receiver/commit/ec66851b7d8eb2ee97e4f89cdc540f408552ea36))
* **server:** end cleared alerts at last update time ([620b8aa](https://github.com/andrewheberle/onms-grpc-receiver/commit/620b8aa6f6cf04087cfa9a4c2733208c1ef43b7d))
* **server:** end firing alerts at receive time plus resolve timeout ([620b8aa](https://github.com/andrewheberle/onms-grpc-receiver/commit/620b8aa6f6cf04087cfa9a4c2733208c1ef43b7d))
* **server:** flush queued alarms on shutdown ([#130](https://github.com/andrewheberle/onms-grpc-receiver/issues/130)) ([45c8e0b](https://github.com/andrewheberle/onms-grpc-receiver/commit/45c8e0b78c68c46018f8265ec2ea1332efb5e9a2))
* **server:** reject both alertmanager URLs and SRV record ([ec66851](https://github.com/andrewheberle/onms-grpc-receiver/commit/ec66851b7d8eb2ee97e4f89cdc540f408552ea36))
