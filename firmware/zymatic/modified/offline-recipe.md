# Offline brewing on the Zymatic

USB setup can install **one selected Zymatic program** in the brewer’s application flash. If no recipe is selected for initial conversion, it installs the archived American Brown Ale schedule. The selected copy stays available after a restart or an OEM settings reset. Select **Brew Recipe** on the main menu to load recipes from the configured server. If the sync yields an empty menu, the brewer can stage the local recipe and display it in the original physical recipe chooser. Selecting the recipe launches the stock brew controller. The separate “Back” choice exits. There is no preloaded grain, water or hops.

On a previously commissioned 1.1.14 machine, a failed network startup can offer **Set up network** or **Continue offline**. The offline choice requires stored account information, the expected commissioning revision, an idle-session marker and a clear stored fault mask. The normal hardware setup and interrupted-session decisions still run. A fresh or faulted unit follows the existing setup path.

## Select a recipe during USB setup

Use a schedule prepared for the Zymatic. The USB tools accept a JSON file through `--offline-program PATH`. The top level must specify `"model": "zymatic"`, a recipe name and a `steps` array. Each step must contain `name`, `target_f`, `minutes`, `location` and `drain_minutes`. The optional `id` is a 32-character lowercase hexadecimal program ID. Omitting it derives an ID from the name and complete schedule.

Native routes are 0 for pass-through, 1 for mash, 2–5 for hop cages 1–4, and 6 for the physical pause. A pause must have a following step, whose name appears in the confirmation screen. New recipes use zero target/time/drain for a pause. Original schedules can carry unused values in those fields. Limits are 1–25 steps, 207°F, 255 minutes per duration and 511 bytes of stock wire data. The builder validates every field and computes the complete application CRC after inserting the program. The Hub can sync other recipes normally. Changing the persistent, offline copy requires USB provisioning again.

## Default American Brown Ale and loading

The catalog original is “Base Recipe - American Brown Ale”, record `chief-zymatic-8364de2ed85a2339`. The recipe specifies 3.55 US gal starting water and an estimated 2.5 US gal batch. Its grain bill is 5 lb American two-row pale, 0.5 lb each Belgian Carapils, American Crystal 40L and American wheat, and 0.25 lb each American chocolate and British brown. The dry hops in the source are for fermentation and go outside the machine program. [Archived source record](https://github.com/jeffreydwalter/picobrew_pico/blob/176b9fa2e7245a8504120b50e327b51d96d1cf2b/app/static/support/base_recipes/zymatic/Base_Recipe_-_American_Brown_Ale.json)

| Hop cage | Contents | Time before boil ends |
| --- | --- | --- |
| 1 | 14.17 g Northern Brewer | 60 min |
| 2 | 14.17 g Northern Brewer | 15 min |
| 3 | 28.34 g Cascade | 10 min |
| 4 | 14.17 g Cascade and 14.17 g Centennial | 2 min |

The preserved schedule heats to 152°F, mashes for 90 minutes, drains for eight, then heats to 207°F. It routes the four hop cages for 45, 5, 8 and 2 minutes in order, then drains for ten minutes. The original physical pause asks the brewer to connect the chiller before a 65°F, ten-minute final step and ten-minute drain. The source gives the fermentation and dry-hopping instructions.

## Sessions and qualification

The provisioned local program is reloaded from flash using the original 1.1.14 tokenizer, step-name allocator and numeric parser. The physical arm positions, sensors and safety controls run in the stock controller. A local session has no remote reset point. Firmware marks its identity before setting the running-brew flag. On power loss, the stock recovery path receives a failed recovery result for that local session. A previous remote session cannot be substituted. Previously interrupted remote sessions retain their ordinary server recovery path.

The isolated AVR checks exercise cold startup with disconnected interfaces, physical menu selection and Back, decoded default and selected recipe steps, the pause route and interrupted-session identity. They simulate peripheral boundaries and do not qualify a physical installation or brew. [OEM reconstruction and replay](../reverse-engineered/README.md)
