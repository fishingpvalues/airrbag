# Changelog

## [0.4.0](https://github.com/fishingpvalues/airrbag/compare/v0.3.1...v0.4.0) (2026-10-04)


### Features

* **proxy:** /airrbag redirects to the dashboard ([#11](https://github.com/fishingpvalues/airrbag/issues/11)) ([1d7bf06](https://github.com/fishingpvalues/airrbag/commit/1d7bf0697387a9ac796212602f4e58c2e925a07e))

## [0.3.1](https://github.com/fishingpvalues/airrbag/compare/v0.3.0...v0.3.1) (2026-10-04)


### Bug Fixes

* **config:** judge inline secrets on the parsed config ([#9](https://github.com/fishingpvalues/airrbag/issues/9)) ([f9c1224](https://github.com/fishingpvalues/airrbag/commit/f9c122411e545fbfaf0b62acf01a61c1bbfcd3de))

## [0.3.0](https://github.com/fishingpvalues/airrbag/compare/v0.2.0...v0.3.0) (2026-10-04)


### ⚠ BREAKING CHANGES

* **security:** the X-Airrbag-Override header and ?airrbagOverride= are ignored unless guard.allow_override_header is true; POSTs to /__airrbag/api need X-Airrbag-Request: 1; the config file must not be group/world-readable.

### Features

* **security:** delegated auth, bound grants, secret hygiene, scanners ([#7](https://github.com/fishingpvalues/airrbag/issues/7)) ([3c80b8c](https://github.com/fishingpvalues/airrbag/commit/3c80b8cc441e8039be051fdcce10721987c14d9b))

## [0.2.0](https://github.com/fishingpvalues/airrbag/compare/v0.1.0...v0.2.0) (2026-10-04)


### Features

* dashboard, logo, and guard refusals the *Arr UI can show ([c6ce314](https://github.com/fishingpvalues/airrbag/commit/c6ce3140d1e8221889e1d50af144f33acd25e73d))
* dashboard, logo, and guard refusals the *Arr UI can show ([39527b0](https://github.com/fishingpvalues/airrbag/commit/39527b00d3dfaeb4306d7e93eb646335300b933f))
* more download clients and evidence sources; unknown is not safe ([#6](https://github.com/fishingpvalues/airrbag/issues/6)) ([89d7df4](https://github.com/fishingpvalues/airrbag/commit/89d7df41729b863d246f160ea7bf69e39324cd04))


### Bug Fixes

* **web:** authenticate like the *Arr UI; layout fixes; screenshots ([1ef7dbf](https://github.com/fishingpvalues/airrbag/commit/1ef7dbf34460e32c3d3e3b3e7d9ca35abacf0c48))

## 0.1.0 (2026-10-04)


### Features

* reverse proxy that guards private-tracker seeds in the *Arrs ([5f13623](https://github.com/fishingpvalues/airrbag/commit/5f136230f1051492a5cd149837b7cdc757c5f8b8))
