# Beer Crafter in the Hub

The shared recipe wizard follows PicoBrew’s grain, mash, boil, hop-bin and yeast workflow. Open **Recipes → Add recipe → Create for a brewer**. Review the grain and hop bills, predicted beer statistics and wort-temperature curve before saving. **Advanced editor** lets you edit ingredients and plan the brew, fermentation and carbonation. Choose **Add recipe → Create by batch size** to start without selecting a brewer. The batch defaults to 2.5 US gallons (9.46 L), the most frequent size in the original PicoBrew public recipes. Enter a different yield when writing or transcribing a recipe of another size. In a saved recipe, **Recipe view** switches between editing the base recipe and showing the quantities and schedule for each brewer.

| Original Crafter task | Where to do it |
| --- | --- |
| Pick a style, build a grain and hop bill, choose yeast | Start typing an ingredient or style in the wizard or editor. Choosing a reference fills its known properties. Custom names and measured ingredient properties are supported. Set recipe measurements to metric or US customary. |
| Compare the grain and hop bills | Each pie represents the bill by weight. Point at a slice or focus a legend item for the ingredient weight and see its percentage in the legend. |
| Compare a recipe with a beer style | Recipe analysis compares OG, FG, ABV, IBU and color with the selected historical style. When sending a recipe, the Hub scales all measured ingredients together to fit the selected brewer. |
| Program mash rests, boil and hops | The wizard offers the model default, infusion, mash-out, high efficiency and custom rests. Each loaded hop slot can hold multiple varieties and receive successive timed visits. For first wort hopping, schedule an addition at the beginning of the boil and select the option to route heat-up through that slot. In the advanced schedule, edit individual targets (including boil temperature), holds, drains and routes. |
| Plan whirlpool, late additions and fermentation | Add a whirlpool hop stand from the advanced step menu. Record aroma or dry hops in the ingredient bill. Choose a yeast and edit attenuation, fermentation temperatures and durations. Plan chilling and carbonation separately. Cooling steps require a compatible controller or manual execution. |
| Estimate starting water and track amendments | Enter the pre-boil volume, measured absorption and equipment loss. Apply the estimate to the water ingredient. Multiple existing water sources can be edited individually. Record amendment quantities and water chemistry. |
| Review, keep, share or print | Review the planned temperature curve and print the brew sheet. The local library supports revisions, copies, JSON and BeerXML exchange, community recipe browsing and machine-specific transfer previews. Native programs retain their original schedule and have a curve and print view. |

## Reference data and estimates

The bundled reference contains 228 fermentables, 179 hops, 309 yeasts, 110 adjuncts, five amendments and 200 historical style entries. These are factual fields exposed by [PicoBrew Beer Crafter](https://picobrew.com/z_crafter/z_recipe). Style names retain their published guide and edition. The [Zymatic manual, recipe design section](https://picodevblob.blob.core.windows.net/content/zymatic/Zymatic_Manual.pdf) describes scaling, bin operation, mash selection and fermentation planning.

Gravity uses fermentable extract potential (46 gravity points per pound per US gallon for sucrose), finished volume and brewhouse efficiency. Sugars and extracts contribute without a mash-efficiency deduction. Final gravity uses the entered apparent attenuation. Mixed strains require a common attenuation to produce one estimate. Alcohol uses the gravity difference times 131.25. Color uses the Morey formula. Bitterness uses Tinseth for timed boil and first-wort hops with pre-boil gravity when the volume is known. Dry hops and whirlpool additions carry no assumed utilization. Missing ingredient properties leave dependent estimates blank. PicoBrew’s server-side brewing predictions and equipment loss calibration are unavailable. [Calculation reference](https://docs.brewfather.app/recipes/calculations)

The forecast starts at 55 °F and uses Crafter’s approximate 4.96 °C/min heating and 0.585 °C/min cooling rates unless the recipe specifies a ramp. Pauses have unknown wall-clock duration. Unheated stretches have unknown temperature. The live brew journal uses measured temperatures. Starting-water estimates use the brewer’s own measured losses.

Pico S, Pro and C use their PicoPak routing. The Hub compiles ingredient recipes for Z using its keg, mash basket and hop compartments. Zymatic supports planning, export and [stock recipe sync](stock-zymatic.md). PicoStill and PicoFerm retain their distillation and fermentation controls. Hosted account publishing and KegSmarts fermentation control are outside the local Hub’s supported integrations.

The brewer ingredient view, printed brew sheet and shopping list show the catalog yeast strain even when the source only names the yeast. An unspecified pitch has no numerical quantity to scale. A measured yeast ingredient supplies the pitch for that library recipe. Hub BeerXML extensions retain fermentable placement, addition time and the published source strain across Hub imports.

The wizard review follows the actual sequence of mash, boil and hop visits. Boil times count down. A return visit circulates through the existing slot load. Wizard-generated ingredient recipes for standalone S, Pro, C and Z modules allow up to 15 steps. The stock Zymatic sync format allows 25, with a 255-minute limit per hold and a maximum target of 207 °F.

The style picker includes all 200 guide-and-code entries in PicoBrew’s Beer Crafter, plus styles used in saved library recipes. Entering a custom style leaves its analytical targets unspecified.

## Machine programs

In **Recipes → Add recipe → Create a machine program**, choose the intended brewer. Pico S, Pro and C use direct keg, mash and adjunct routes. The stock Zymatic editor uses its menu protocol, including operator pauses. Z Program Maker supports active steps, operator pauses, beer setup and the PicoPak adjunct setting. Locations 1 and 2 on a Z are the keg and mash basket. Locations 3–6 select hop compartments 1–4. The file records Celsius and seconds regardless of the units displayed in the editor.

Import original Program Maker JSON from the library. The Hub can edit programs with up to 20 steps. **Send** checks the connected standalone Z’s reported limit and verifies the saved program by reading it back. You can start the program when you are ready.

Use **Export Z program** for stock 0.0.119. Its receiver allows 2,047 bytes and 64 JSON entries, so the number of steps that fit depends on their contents. For example, 20 named pauses with no duration or unit preference fit exactly 64 entries. The stock website adds a time field to each pause when it sends a file, which can put a previously fitting program over the receiver’s limit.

An archived Z schedule uses a different recipe format. Copy it to the library and send it directly to compatible standalone Z firmware. For an original PicoPak, you also need the physical pack and the Z PicoPak Adapter. The kit record supplies its strength and bitterness settings and any adjustable timing ranges. Turning on the Program Maker PicoPak setting only changes adjunct routing. [Z recipes and front panel](family-devices.md#z-recipes-and-front-panel)

## Ingredient sources

The 1,316 public ingredient recipes in the bundled catalog are archived PicoBrew public recipes. Their detail pages link to the corresponding [PicoBrew Public Recipes](https://picobrew.com/publicrecipes/publicrecipes) record by GUID. Recipes originating in kits retain their original machine schedules. Published ingredient names and styles for matched kits are stored separately in [Marketplace facts](../internal/catalog/data/marketplace.json), with a link to each [PicoBrew Marketplace](https://picobrew.com/BrewMarketplace/Index.cshtml) product. Each match requires the kit name and the original PicoBeerID across every grouped source record. The generic ID 50 is excluded. Published names without weights are displayed as ingredient references and do not contribute invented quantities to shopping lists, brewing calculations or machine instructions.

### Brew-ready catalog

**Browse all** shows recipes with measured ingredients and a program for every brewing profile. Open a recipe and select a brewer under **Machine versions** to see its ingredient bill and program. Reviewed stock Zymatic loading sheets and original programs are available on the corresponding recipe pages. **Sources & credits → Browse source archive** opens the original schedules, including discontinued PicoPaks without measured ingredient bills.

Saving in the wizard or advanced editor prepares versions for Pico C, Pico S, Pico Pro, generic Z-series and stock Zymatic. The Hub also translates imports and existing library recipes. Each version scales every measured ingredient together up to the grain, hop slot or saved usable batch limit. Equipment efficiency informs gravity estimates. The original recipe is the editable source for all five. Single and bulk transfers and the stock menu preview generate the same target program with the selected batch and current calibration. A blocked version shows the ingredient bill and the reason its schedule needs editing.

For qualified recipes, **Save Zymatic program** saves the original routing and brew sheet together for the stock menu. **Copy ingredient recipe** creates an editable planning copy. Its declared mash/boil plan can be adapted independently. Source gravity, color and other published targets are shown where available. Manufacturer pitching ranges are labeled and linked separately from the original ingredient quantities.

The offline index is reproducible with `python3 server/tools/catalog_brew_sheets.py --check`. It checks the measured grain and hop bill against the source's human loading sheet, the countdown at which each cage is wetted, mash temperatures and durations, and stock program and brewer capacities. The current coverage includes single-load all-grain Zymatic programs with an identified supported yeast and a documented pitch rate. Unresolved ingredients, multiple loads, and incompatible or independently edited schedules can still be found in the archive. A passing check describes the recipe data and stock wire format. Physical brews are unverified.

Each qualifying source revision also has a review in `server/tools/catalog-brew-reviews.json`. The review checks the author's free-text notes for additions and changes to the structured recipe. Measured additions recovered from those notes appear in the editable ingredient bill and travel with the stock program's brew sheet. A changed source hash requires a fresh review.
