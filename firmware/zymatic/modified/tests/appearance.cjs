// Run the settings code actually embedded in each AVR application.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { gunzipSync } = require("node:zlib");

for (const name of ["zymatic_compact_site", "zymatic_site"]) {
  const header = fs.readFileSync(path.join(__dirname, "../site", name + ".h"), "utf8");
  const bytes = Buffer.from([...header.matchAll(/0x([\da-f]{2})/g)].map((m) => parseInt(m[1], 16)));
  const html = gunzipSync(bytes).toString();
  assert.match(html, /<summary>Settings<\/summary>/);
  assert.match(html, /<label>Appearance ?<select id="theme">/);
  assert.deepEqual([...html.matchAll(/<option value="(\w+)">(\w+)/g)].map((m) => m.slice(1)), [
    ["system", "System"], ["light", "Light"], ["dark", "Dark"],
  ]);
  assert.match(html, /color-scheme:light dark/);
  const script = [...html.matchAll(/<script>(.*?)<\/script>/gs)].find((m) => m[1].includes("picobrew-theme"))[1];
  function load(storage) {
    const picker = {
      value: "system",
      get selectedIndex() { return ["system", "light", "dark"].indexOf(this.value); },
    };
    const style = {};
    vm.runInNewContext(script, {
      document: { getElementById: (id) => { assert.equal(id, "theme"); return picker; }, documentElement: { style } },
      localStorage: storage,
    });
    return {
      picker, style,
      change(value) { picker.value = value; picker.onchange(); },
    };
  }
  const storage = {};
  const page = load(storage);
  assert.equal(page.picker.value, "system");
  for (const value of ["dark", "light", "system"]) {
    page.change(value);
    const restored = load(storage);
    assert.equal(restored.picker.value, value);
    assert.equal(restored.style.colorScheme, value === "system" ? "" : value);
  }
  assert.equal(load({ "picobrew-theme": "invalid" }).picker.value, "system");
  const blocked = new Proxy({}, {
    get() { throw Error("Storage blocked"); }, set() { throw Error("Storage blocked"); },
  });
  const privatePage = load(blocked);
  privatePage.change("dark");
  assert.equal(privatePage.style.colorScheme, "dark");
}
