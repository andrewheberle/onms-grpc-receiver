# Changelog

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
