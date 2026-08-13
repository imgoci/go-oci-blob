# Changelog

## [1.1.1](https://github.com/imgoci/go-oci-blob/compare/v1.1.0...v1.1.1) (2026-08-13)


### Bug Fixes

* clamp elapsed Retry-After dates to zero ([#35](https://github.com/imgoci/go-oci-blob/issues/35)) ([5eeca6d](https://github.com/imgoci/go-oci-blob/commit/5eeca6d701739bbe6624c95befc2f842af22db87))

## [1.1.0](https://github.com/imgoci/go-oci-blob/compare/v1.0.0...v1.1.0) (2026-08-13)


### Features

* expose retry and registry error metadata ([#30](https://github.com/imgoci/go-oci-blob/issues/30)) ([fb05287](https://github.com/imgoci/go-oci-blob/commit/fb052878ee41b803fa5799bc04df9bb164b8fdf9))
* harden upload destinations and redirects ([#32](https://github.com/imgoci/go-oci-blob/issues/32)) ([a741861](https://github.com/imgoci/go-oci-blob/commit/a74186131e9c8ff4d1b91d3b2415f7044843695c))
* report upload wire consumption ([#33](https://github.com/imgoci/go-oci-blob/issues/33)) ([92cdbb0](https://github.com/imgoci/go-oci-blob/commit/92cdbb042e32a88a17f41c41067ab44d0714abeb))

## 1.0.0 (2026-08-13)


### Features

* add client skeleton and blob existence check ([#10](https://github.com/imgoci/go-oci-blob/issues/10)) ([93dc58c](https://github.com/imgoci/go-oci-blob/commit/93dc58c0b587210a445c9c33a1621e6b2f6f76d5))
* add monolithic push and cross-repository mount ([#13](https://github.com/imgoci/go-oci-blob/issues/13)) ([6af7ba4](https://github.com/imgoci/go-oci-blob/commit/6af7ba46abf1a4b3e0c854e524311be9da83026f))
* add parallel pull behind WithParallelPull ([#16](https://github.com/imgoci/go-oci-blob/issues/16)) ([ac990d4](https://github.com/imgoci/go-oci-blob/commit/ac990d4566802136f0b531368b593578b1d1319f))
* add verified chunked upload behind WithChunkedUpload ([#15](https://github.com/imgoci/go-oci-blob/issues/15)) ([395712c](https://github.com/imgoci/go-oci-blob/commit/395712c838ef06d501edf4330e0c2119ce5e4259))
* add verified pull and unverified ranged pull ([#12](https://github.com/imgoci/go-oci-blob/issues/12)) ([7f58c57](https://github.com/imgoci/go-oci-blob/commit/7f58c57d78c6c808a756f366131a2c9ea539a6f4))
* report transfer progress via WithProgress ([#17](https://github.com/imgoci/go-oci-blob/issues/17)) ([d6c256d](https://github.com/imgoci/go-oci-blob/commit/d6c256d52f03eafe3cda31e2753e9c5128a8b335))
* retry transient failures, resume pulls, restart pushes ([#14](https://github.com/imgoci/go-oci-blob/issues/14)) ([e2e62fa](https://github.com/imgoci/go-oci-blob/commit/e2e62faa52c36f09ecbe466c2998339c2efdfb96))


### Bug Fixes

* clarify progress callback concurrency contract ([#24](https://github.com/imgoci/go-oci-blob/issues/24)) ([8700a09](https://github.com/imgoci/go-oci-blob/commit/8700a0989bb82ca272ca986e2dc8eae79536d1b5))
* harden OCI transfer correctness ([#20](https://github.com/imgoci/go-oci-blob/issues/20)) ([c9b5bd1](https://github.com/imgoci/go-oci-blob/commit/c9b5bd159eca2d972a5add281fc96b2876ba5d1f))
* harden transfer failure handling ([#29](https://github.com/imgoci/go-oci-blob/issues/29)) ([064105c](https://github.com/imgoci/go-oci-blob/commit/064105c4e9162aa71bee2b3c76439733bf70d545))


### Performance

* remove transfer hot-path bottlenecks ([#21](https://github.com/imgoci/go-oci-blob/issues/21)) ([0251e8c](https://github.com/imgoci/go-oci-blob/commit/0251e8cead2021022fbfc1b188eeeff1030172cc))
