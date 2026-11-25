# License boundaries

This repository contains code and data with different provenance. Organizing
firmware by machine and packaging the Hub server does not apply one new license
to every component.

| Scope | License or notice |
| --- | --- |
| Exact S/Pro OEM reconstruction in `firmware/pico-s-pro/reverse-engineered/` | Original [LGPL-2.1 notice](firmware/pico-s-pro/reverse-engineered/LICENSES/LGPL-2.1.txt), [TI notice](firmware/pico-s-pro/reverse-engineered/LICENSES/TI-BSD-notice.txt), per-file notices and [dependency provenance](firmware/pico-s-pro/reverse-engineered/dependencies/lock.json) are preserved with the exact snapshot. |
| Modified S/Pro firmware in `firmware/pico-s-pro/modified/` | Retained [LGPL-2.1 notice](firmware/pico-s-pro/modified/LICENSES/LGPL-2.1.txt), [TI notice](firmware/pico-s-pro/modified/LICENSES/TI-BSD-notice.txt), per-file notices and [dependency provenance](firmware/pico-s-pro/modified/dependencies/lock.json). These component notices are not a blanket license for every file. |
| Isolated web recovery candidate in `firmware/pico-s-pro/modified/recovery/` | Its original [notices](firmware/pico-s-pro/modified/recovery/LICENSES/), per-file notices and [pinned dependency provenance](firmware/pico-s-pro/modified/recovery/dependencies/lock.json) are retained. The [import record](firmware/pico-s-pro/modified/recovery/import-provenance.json) distinguishes preserved originals from new integration files; this copy assigns no new blanket component license. |
| Original device image and upstream files under `firmware/*/oem/` | Original PicoBrew firmware and embedded assets retain their existing rights. This import assigns no new license. The [upstream manifest](firmware/upstream-manifest.json) records source attribution and exact file identities. |
| Z-series selected W5500 rebuild dependency | [Adafruit Ethernet2 at pinned commit](https://github.com/adafruit/Ethernet2/tree/0bab6349fb5b42fce2d5442bbed093279184b548), with original per-file GPL/LGPL alternatives and `license.txt` retained in acquired and adapted build trees. [Exact file identities and provenance](firmware/z-series/reverse-engineered/dependencies/ethernet2.json); this dependency is selected for the rebuild and is not identified as the vendor's exact fork revision. |
| Browser favicon shipped with the Hub and local device websites | Original PicoBrew artwork from [PicoBrew’s website icon](https://picobrew.com/favicon.ico). The source ICO has SHA-256 `63cb852c23c1b1bda83e133dcb7e070aabb3e594531196ddeab8e23d32baa4ea`. PicoBrew retains its rights to the artwork. |
| Hub application code in `server/` | [MIT license](server/LICENSE), copyright 2026 PicoBrew Hub contributors. Dependencies retain their own licenses. |
| PDF manual renderer and its bundled resources | PDF.js is pinned in the [frontend dependencies](server/web/package.json). Its upstream license and the font/decoder notices are copied unchanged into the built website under `vendor/pdfjs-<version>/`, alongside the locally served resources. |
| `server/internal/catalog/data/catalog.zip` and `firmware/pico-s-pro/modified/data/starter-recipes.json` | Pinned recipes, machine programs, illustrations and original notices. Attribution and license metadata remain in the catalogue manifest and recipe provenance. The server's MIT license does not relicense these records or assets. |
| `server/web/src/data/beer-crafter.json` | Factual ingredient measurements and historical style ranges from [PicoBrew’s original Beer Crafter](https://picobrew.com/z_crafter/z_recipe). Style codes and ranges are credited to the guides identified in the data, including BJCP. See the [source and calculation notes](server/docs/beer-crafter.md). |
| Original project contributions to standalone firmware, device websites, tests, documentation and integration tools under `firmware/common/`, `firmware/*/modified/`, `docs/` and `tools/` | The [project MIT license](LICENSE) covers these contributions. Upstream code, reconstructed vendor material, SDKs, dependencies, recipes, manuals and artwork retain their respective terms and notices, including in mixed-source files and compiled images. |

The earlier C decompiler exports under the S/Pro source trees' `legacy/`
directories retain their own notices. As recorded in the
[firmware README](firmware/pico-s-pro/modified/README.md), the reconstruction
does not assign a new license to PicoBrew's original binaries or embedded
assets. Copying the complete Chiefwigms firmware archive does not change that.

Repository-wide content licenses for the support/manual mirror, PicoPak
catalogue and recipe/site archive were not established during the catalogue
work. Source attribution, original license files and applicable per-file
notices remain with the material. Chiefwigms MIT notices and existing icon
attribution are retained in the bundled catalogue; the historical Python
Zymatic server is not included as an application.

See [community sources](docs/community-projects.md) and the
[catalogue guide](server/docs/community-catalog.md) for source identities and
content boundaries. Public availability alone does not establish a license
grant.
