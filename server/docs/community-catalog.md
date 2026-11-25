# Offline community catalog

The Hub bundles a pinned archive of recipes, machine programs, illustrations and
support references. Browsing and recipe copying work without picobrew.com or an
upstream server. The source archive is read-only; **Copy to library** creates an
independent editable recipe with its own revision history. Copying never sends a
recipe to a brewer or starts a program.

## Included content

| Source | Original records | Catalog entries | Treatment |
| --- | ---: | ---: | --- |
| Public PicoBrew recipes | 1,320 | 1,316 | Ingredient recipes with source quantities, declared mash/boil plans and conversion warnings; four reviewed disposable records are hidden. |
| PicoPak/ZPak library | 10,161 | 3,863 | Native machine programs. Exact duplicates are grouped; 62 reviewed junk entries are hidden and all originals remain archived. |
| Chiefwigms server | 37 | 37 | Base programs and three Zymatic maintenance programs, with original notes. |

The main catalog lists 975 recipes whose schedules compile for Pico S, Pico Pro, Pico C, Z and Zymatic. These include 127 named Hub adaptations. The source archive also includes recipes that need manual review. The 68 reviewed stock Zymatic brew sheets stay attached to their source recipes. The complete source archive contains **5,216 browsable entries**: 1,316 ingredient recipes, 3,897 native
programs and three maintenance programs. Of these, 5,206 can be copied into the
library. There are 3,694 programs within Pico S/Pro wire limits; deployment also
requires compatible firmware, verified device identity and sufficient storage.

The PicoPak archive contains 247 malformed JSON records and 107 empty programs.
They are excluded from browsing and preserved byte-for-byte in the embedded
source archive and `excluded.json`. Entries with long or non-ASCII machine labels
remain copyable, but require explicit editing before deployment; labels and
timings are never silently rewritten during native import.

The bundle also contains 88 image/text assets, including beer color/style
illustrations, machine drawings and support text. Illustration credits and kinds
are available in source details; they are not photographs of named beers. An additional
27 original PDF assets are optional local references. The references API reports
which files are actually available.

## Recovered recipes

The Hub checks archived loading sheets against the ingredient list and the first flow to each occupied slot in the original machine program. Ingredients loaded together share the slot’s weight limit. A confirmed first-wort addition receives flow during heat-up. Capacity errors in the original batch can be resolved by scaling all measured ingredients together.

All 440 ingredient recipes previously outside the main catalog have an individual review pinned to the original record's SHA-256. The author's explicit corrections take precedence. The reviewer also compared ingredient amounts, original cage loads, and the machine's actual time at each temperature and flow path. In *Down with the Brown*, a zero boil-temperature field conflicts with three timed segments at 207°F. Those segments account for the entire 90-minute boil and all three hop additions.

Of those 440, the first review recovered 23 recipes. Further review of saved programs recovered two more from the source. Another 127 now have a **Hub adaptation** that names the brewing choices needed to make the recipe usable. The other 288 have individual notes describing what remains unresolved. Some records contain conflicting hop timings or an operator-dependent action that the machine program cannot represent. Others are cleaning routines or lack enough information to reconstruct the beer.

Open a recipe to see the assumptions before copying it. For *Heinielicious*, the original calls for Irish moss without giving a unit. The **Unfined** edition omits it and explains the possible effect on clarity. For a weighed Irish moss edition, the review identifies the product and the volume-to-weight estimate. Where a published reference supplies brewing-water targets or hop contact time, the review links to that reference. **View original recipe** opens the unmodified local record. A library copy keeps the edition in its name and the assumptions in its notes.

Beer Crafter printed ounces on US loading sheets for some adjuncts that had no recorded unit. Correcting that interpretation returned 53 previously listed recipes to the archive. Across all 1,316 source ingredient recipes, 975 qualify for the main catalog and 341 are available in the archive. Software validation covers ingredient units, brewer capacity and program encoding. Physical brewing and flavor have not been verified for these editions.

Open **Recipes → Browse all → Sources & credits → Browse source archive** and select **Reviewed recipes** for the original group of 440. The 53 other records and their warnings are also available under **All source records**, filtered to public recipes. The 68 original stock Zymatic brew sheets have separate verification criteria. Existing library copies retain the owner's edits. Copy the catalog entry again to obtain its reviewed version.

Run `python3 server/tools/catalog_recovery.py --check` to reproduce the index from the bundled originals and checksum-pinned reviews.

## Catalog curation

A review of all 5,282 previously browsable entries removed **66 clearly
disposable entries representing 498 original records**: explicit test/default
placeholders, keyboard-mash names, unheated test fixtures, three diagnostic-only
ingredient records and one unnamed draft with a proven identical named successor.
Named experimental beers and functional maintenance/extraction programs remain.
The [review summary](catalogue-curation.md) and
[exact curation manifest](../tools/catalog-curation.json) record each decision,
reason, evidence and original checksum. Changed source records require a new
review. Every original remains archived byte-for-byte; `excluded.json` now
accounts for 852 records, including the 354 malformed or empty records excluded
before this review. Saved library recipes, revisions and installed brewer
programs are not removed or rewritten.

The normal source builder applies these explicit decisions before writing its
index. `catalog_curation.py` also reproduces the same bundle from the exact
original ZIP hash recorded in the review, without downloading source data.

## Recipe semantics

**Ingredient recipes** retain converted source measurements and declared plans.
The original public recipes target Z/Zymatic, and their separately editable
`MachineSteps` may disagree with those plans. They remain in original JSON.
The Hub preserves the source flow paths and generates a separate version for each brewer from the declared mash rests and timed additions. Ingredients added together share a hop slot. Unsupported operations and incomplete measurements appear on the affected version. Unknown yeast
doses, unsupported quantities, fractional/invalid timings and contradictory
source notes are recorded as review items. Named Hub adaptations state how any missing quantities or timings were chosen.

**Native programs** retain their exact temperatures, timings, drain durations and
model-specific compartment routing. They do not acquire an inferred ingredient
bill, batch size or efficiency, and cannot be scaled by batch volume. A zero-minute
step retains zero: it waits for its temperature target and advances. The target
firmware must advertise `zero_minute_steps` support before accepting such a
transfer. Copying preserves the native program ID so the transfer preview can
recognize a program already present on the brewer.

The ten chiefwigms Pico base recipes use named locations and a separate `Prime`
step whose translation is not verified here. Their notes, illustrations and
original JSON are browsable, but automatic copying/deployment is disabled.
The 39 archived Z schedules run on standalone Z modules that report original Z schedule support. The Hub also transfers compatible original PicoPak programs to a Z with kit support, for use with the pack and adapter. A stock Zymatic can retrieve a compatible published program from its sync menu. A stock Z on 0.0.119 can import a compatible Program Maker export through its own website. [Z recipe formats](family-devices.md#z-recipes-and-front-panel) · [Stock Zymatic setup](stock-zymatic.md)

Every entry carries its repository, pinned commit, source path, SHA-256,
attribution/license notice and conversion warnings. Grouped PicoPak entries expose
each original record independently. Public recipe statistics come from the source. On compatible original PicoPak records, `Abv` and `Ibu` are kit strength indices. Where the author supplied adjustment ranges, the Z uses `MinTime` and `MaxTime` to change contact times. Measured alcohol percentage and bitterness are separate recipe data.

The recipe pages round quantities and temperatures for readable display without
changing the archived measurements. Sources and credits are available in an
expandable section. **Machine Guides & Artwork** opens bundled Markdown in a
reader dialog with tables, images, headings and internal navigation. Links to
other bundled references stay in the reader; external resources remain source
links. Optional manuals open in the reader when their verified local PDF is
installed, with an archived-source link shown when it is absent.

## API

All endpoints require the normal owner session; copying also requires CSRF
protection.

| Method and path | Result |
| --- | --- |
| `GET /api/v1/catalog` | Search with `q`, `source`, `kind`, `model`, `style`, `offset` and `limit` (default 24, maximum 100). The default and `kind=all-brewers` return recipes with a program for every brewing profile. Use `kind=recipe` for every source ingredient recipe. Use `kind=reviewed` for recipes individually reviewed against the archived author records, including those with unresolved instructions. Use `kind=brew-ready` for verified stock Zymatic programs or `kind=archive` for all source records. Returns entries, total and source/style metadata. |
| `GET /api/v1/catalog/{id}` | Recipe/program detail, original record list, provenance and source parameters. |
| `POST /api/v1/catalog/{id}/copy` | Send `{}` for the editable recipe or `{"stock_program":true}` for a qualified stock Zymatic program with its brew sheet. Returns a new library record and initial revision. |
| `GET /api/v1/catalog/{id}/source` | Exact original JSON attachment; `record` selects another original listed on the entry. |
| `GET /api/v1/catalog/references` | Support references and asset metadata, optionally filtered by `model`. |
| `GET /api/v1/catalog/assets/*` | Manifest-listed local image/text/PDF, with pinned checksum verification. |

No catalog endpoint fetches arbitrary URLs. Source paths are selected from the
entry manifest, assets are selected from the asset manifest, and optional files
are constrained to the reference directory and checked against their pinned hash.

## Rebuild the pinned bundle

Normal installation needs no source checkouts or catalog-generation step. The
Go executable embeds `internal/catalog/data/catalog.zip`.

To regenerate it, check out the exact four source commits recorded in
`tools/build_catalog.py` into a single directory, using the configured folder
names (`picobrew-recipes`, `picopak-library`, `picobrew_pico` and
`picobrew-support`). From the community repository root:

```sh
python3 server/tools/build_catalog.py \
  --sources /path/to/pinned-source-checkouts \
  --references-output /tmp/picobrew-reference-bundle.zip
CATALOG_REQUIRE_FIRMWARE=1 python3 -m unittest discover -s server/tools -p 'test_*.py'
cd server
go test ./internal/catalog ./internal/server
```

The builder checks pinned commits and clean tracked files, preserves source bytes,
rejects active SVG content, and writes reproducible ZIP timestamps and ordering.
The three ingredient/glass icons and their two original notices are checked in
under `server/tools/reference-icons/` and verified against pinned SHA-256 hashes.
No historical Zymatic server checkout or implementation is required to rebuild
or run the Hub. The archive retains those assets' original source paths and
attribution.
Python conversion tests use originals already included in the bundle. Go tests
validate every copyable recipe, every advertised deployment, all original source
hashes, exclusion preservation, and exact native temperatures/timings/routing.
The Python suite also compares every firmware starter's ID, complete schedule and
source checksum to its catalog copy in
`firmware/pico-s-pro/modified/data/starter-recipes.json` at the repository root.
`CATALOG_REQUIRE_FIRMWARE=1` makes missing firmware data an error in the project
checkout; an isolated server source archive
can run the remaining tests without the optional cross-component check.

To install optional manuals, extract the generated reference ZIP into the Hub data
directory. It contains the single top-level directory `catalog-references/`.
For Docker, this directory belongs in the existing `/data` volume. Source links
remain visible when a reference is absent; the Hub never downloads it on demand.
Existing recipes, revisions, calibration settings and deployed programs are not
rewritten when the bundled catalog changes.

## Source rights

Source repositories without a repository-wide license are identified as such.
Including provenance does not grant a new license to original PicoBrew data,
manuals or artwork. Chiefwigms MIT notices and the legacy icon attribution are
retained alongside original source files; component license boundaries remain
documented in the repository's `LICENSES.md`.
