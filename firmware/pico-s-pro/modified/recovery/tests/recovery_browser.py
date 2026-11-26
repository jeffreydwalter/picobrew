"""Run the archived recovery suite with CSP-compatible file-read synchronization."""
import unittest

from playwright.sync_api import expect

import test_recovery_web as archived


class RecoveryBrowser(archived.RecoveryBrowser):
    def test_disconnect_during_local_report_read_cannot_mutate_new_connection(self):
        self.connect()
        self.choose(archived.package(web=False))
        self.page.evaluate("""() => {
          const readText = Blob.prototype.text;
          const readBuffer = Blob.prototype.arrayBuffer;
          const signals = document.documentElement.dataset;
          Blob.prototype.arrayBuffer = async function() {
            const result = await readBuffer.call(this);
            signals.oldFileReadFinished = "true";
            return result;
          };
          Blob.prototype.text = function() {
            if (this instanceof File && this.name === "firmware-report.json") {
              return new Promise(resolve => {
                window.releaseReport = () => resolve(readText.call(this));
                signals.reportReadPending = "true";
              });
            }
            return readText.call(this);
          };
        }""")
        self.page.locator("#plan").click()
        expect(self.page.locator("html")).to_have_attribute("data-report-read-pending", "true")
        self.page.locator("#disconnect").click()
        self.connect()
        self.page.evaluate("() => window.releaseReport()")
        expect(self.page.locator("html")).to_have_attribute("data-old-file-read-finished", "true")
        self.page.locator("#refresh").click()
        expect(self.page.locator("#message")).to_have_text("Inventory refreshed.")
        expect(self.page.locator("#plan")).to_be_enabled()
        self.assertEqual(self.posts(), [])
        self.assertEqual(self.c.recovery_http_write_opens(), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
