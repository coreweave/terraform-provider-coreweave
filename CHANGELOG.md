# Changelog

## [0.2.9](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.8...v0.2.9) (2025-06-05)


### Bug Fixes

* retry deadline exceeded errors ([#110](https://github.com/coreweave/terraform-provider-coreweave/issues/110)) ([5bf6099](https://github.com/coreweave/terraform-provider-coreweave/commit/5bf60995b47c2d978e740250b4110cca90cf1ee1))

## [0.2.8](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.7...v0.2.8) (2025-06-03)


### Bug Fixes

* set intermediate state before continuing to poll ([#108](https://github.com/coreweave/terraform-provider-coreweave/issues/108)) ([a00a91e](https://github.com/coreweave/terraform-provider-coreweave/commit/a00a91e2061572cee96e210af40da791fa5ff6cb))

## [0.2.7](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.6...v0.2.7) (2025-06-02)


### Bug Fixes

* update retry policy to allow context.DeadlineExceeded errors ([#106](https://github.com/coreweave/terraform-provider-coreweave/issues/106)) ([be3f77a](https://github.com/coreweave/terraform-provider-coreweave/commit/be3f77aca1b1a934370ab3bb117e627448b2ea81))

## [0.2.6](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.5...v0.2.6) (2025-05-29)


### Bug Fixes

* add retryable-http client ([#104](https://github.com/coreweave/terraform-provider-coreweave/issues/104)) ([e208d97](https://github.com/coreweave/terraform-provider-coreweave/commit/e208d9711b5f0ec06d6cabccbf664c2a9cfb660e))

## [0.2.5](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.4...v0.2.5) (2025-05-28)


### Bug Fixes

* set http.ProxyFromEnvironment in client transport ([#102](https://github.com/coreweave/terraform-provider-coreweave/issues/102)) ([ae63d8b](https://github.com/coreweave/terraform-provider-coreweave/commit/ae63d8b70221e23353023b35718534d581582a33))

## [0.2.4](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.3...v0.2.4) (2025-04-25)


### Documentation

* update vpc resource example ([#100](https://github.com/coreweave/terraform-provider-coreweave/issues/100)) ([cfd65cb](https://github.com/coreweave/terraform-provider-coreweave/commit/cfd65cbabcdd369b249010616ecd165ac98e2ee4))

## [0.2.3](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.2...v0.2.3) (2025-03-17)


### Bug Fixes

* increase cks_cluster resource timeout to 45 minutes ([#97](https://github.com/coreweave/terraform-provider-coreweave/issues/97)) ([60782de](https://github.com/coreweave/terraform-provider-coreweave/commit/60782deec663f45b1e851581a5ffc822d30132ed))

## [0.2.2](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.1...v0.2.2) (2025-03-11)


### Bug Fixes

* update clients, handle cluster creation failures ([#90](https://github.com/coreweave/terraform-provider-coreweave/issues/90)) ([ee5123f](https://github.com/coreweave/terraform-provider-coreweave/commit/ee5123fa369ba5f0d195e6e563c06771d92a5d07))

## [0.2.1](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.2.0...v0.2.1) (2025-03-10)


### Documentation

* add cluster and vpc data source examples ([#87](https://github.com/coreweave/terraform-provider-coreweave/issues/87)) ([5d3079e](https://github.com/coreweave/terraform-provider-coreweave/commit/5d3079e2a1096dd49ec3cc4b3902298a10c0da18))

## [0.2.0](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.8...v0.2.0) (2025-03-10)


### Features

* add coreweave_cks_cluster data source ([#80](https://github.com/coreweave/terraform-provider-coreweave/issues/80)) ([da2e2fc](https://github.com/coreweave/terraform-provider-coreweave/commit/da2e2fcf7b8c16628f23799ca82f3516bee1bfd8))
* add coreweave_networking_vpc data source ([#82](https://github.com/coreweave/terraform-provider-coreweave/issues/82)) ([256e094](https://github.com/coreweave/terraform-provider-coreweave/commit/256e094e0e7fe48b46bf4d9c72f062f87970f4a1))

## [0.1.8](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.7...v0.1.8) (2025-02-28)


### Bug Fixes

* add api_server_endpoint field to cluster resource, handle not found errors correctly ([#77](https://github.com/coreweave/terraform-provider-coreweave/issues/77)) ([e2d7b84](https://github.com/coreweave/terraform-provider-coreweave/commit/e2d7b84d2ed0b8d6494d7464cbf75e50aa53fa0a))
* **docs:** add import syntax ([#73](https://github.com/coreweave/terraform-provider-coreweave/issues/73)) ([f2c9633](https://github.com/coreweave/terraform-provider-coreweave/commit/f2c9633db92ad9257c3824e10a26be97354f1b55))

## [0.1.7](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.6...v0.1.7) (2025-02-21)


### Bug Fixes

* handle resource already exists error ([#69](https://github.com/coreweave/terraform-provider-coreweave/issues/69)) ([a07f3ff](https://github.com/coreweave/terraform-provider-coreweave/commit/a07f3ffc09bbd896342373f98f95b27ff6e4f925))
* provider token initialization, update docs ([#67](https://github.com/coreweave/terraform-provider-coreweave/issues/67)) ([f7995b0](https://github.com/coreweave/terraform-provider-coreweave/commit/f7995b0d0a67042d63bd238ba9acd9ac57f8acec))

## [0.1.6](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.5...v0.1.6) (2025-02-21)


### Bug Fixes

* do not update terraform-registry-manifest on release ([#65](https://github.com/coreweave/terraform-provider-coreweave/issues/65)) ([f307091](https://github.com/coreweave/terraform-provider-coreweave/commit/f3070916d4eb7c823110b572bbc20c3df4d75a2f))

## [0.1.5](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.4...v0.1.5) (2025-02-20)


### Bug Fixes

* update terraform provider with latest vpc api schema changes ([#58](https://github.com/coreweave/terraform-provider-coreweave/issues/58)) ([2e4a8b2](https://github.com/coreweave/terraform-provider-coreweave/commit/2e4a8b2cbe6f1784c719b46526392ee6f94ace75))

## [0.1.4](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.3...v0.1.4) (2025-02-05)


### Bug Fixes

* update cks client ([#50](https://github.com/coreweave/terraform-provider-coreweave/issues/50)) ([e18fbd7](https://github.com/coreweave/terraform-provider-coreweave/commit/e18fbd72867b25759e64cc442d30cef55b4e6d0b))

## [0.1.3](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.2...v0.1.3) (2025-01-31)


### Bug Fixes

* only parse first line of commit message ([#47](https://github.com/coreweave/terraform-provider-coreweave/issues/47)) ([5448b94](https://github.com/coreweave/terraform-provider-coreweave/commit/5448b94541ea4c3c812ea7501426d577c63bc451))
* proper release job permissions ([#48](https://github.com/coreweave/terraform-provider-coreweave/issues/48)) ([4e1a431](https://github.com/coreweave/terraform-provider-coreweave/commit/4e1a4313cc073ad8dd5dc920addc587b45785ee2))

## [0.1.2](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.1...v0.1.2) (2025-01-31)


### Bug Fixes

* update tagging logic to ignore PR numbers in commit message ([#45](https://github.com/coreweave/terraform-provider-coreweave/issues/45)) ([5a3b276](https://github.com/coreweave/terraform-provider-coreweave/commit/5a3b2764bd519eb061df8346bbbb9d67d675cb0d))

## [0.1.1](https://github.com/coreweave/terraform-provider-coreweave/compare/v0.1.0...v0.1.1) (2025-01-31)


### Bug Fixes

* handle quota failure errors ([#30](https://github.com/coreweave/terraform-provider-coreweave/issues/30)) ([d575fef](https://github.com/coreweave/terraform-provider-coreweave/commit/d575fef833bef80b1d797b1359657b520054d929))
* update api clients ([#40](https://github.com/coreweave/terraform-provider-coreweave/issues/40)) ([f3aced3](https://github.com/coreweave/terraform-provider-coreweave/commit/f3aced3d2d78155e3b93e5b8c8376d8ae88bb78e))

## 0.1.0 (2025-01-14)


### Bug Fixes

* add auto-tagging job ([6cceb9b](https://github.com/coreweave/terraform-provider-coreweave/commit/6cceb9be9d66c2b476bd12f6de1d75fb16f899f5))
* add release-please manifest, set initial version to v0.1.0 ([6425edd](https://github.com/coreweave/terraform-provider-coreweave/commit/6425edd3186b72f2302d79a78713221cd8d1cb2c))
* checkout repo in release-please workflow ([994ac8d](https://github.com/coreweave/terraform-provider-coreweave/commit/994ac8d859d5a07829f6f5c2b122f9bdebfd7ff6))
* only run tests on PRs ([5289fb8](https://github.com/coreweave/terraform-provider-coreweave/commit/5289fb8144ac0cfb465be2c08a8fbcaee5371944))
* release-please action ([59f0400](https://github.com/coreweave/terraform-provider-coreweave/commit/59f04000b9af4a45aa4e4035743f034d7af1eea3))
* setup release-please, add license ([fafb773](https://github.com/coreweave/terraform-provider-coreweave/commit/fafb773f50c523c4e10b1ee31d81f14a643f7990))
* specify release-please config file ([b0e9ab8](https://github.com/coreweave/terraform-provider-coreweave/commit/b0e9ab879f828a1cbb9cdaa3b8808637795c6e13))
* upgrade release-please action ([a3387a4](https://github.com/coreweave/terraform-provider-coreweave/commit/a3387a4471484a292839e893101574de485076e7))
