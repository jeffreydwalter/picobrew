PYTHON ?= python3

.DEFAULT_GOAL := recovery
.PHONY: bootstrap recovery test web-test verify verify-imports recovery-verify recovery-web-test clean

bootstrap:
	$(PYTHON) tools/bootstrap_dependencies.py

verify-imports:
	$(PYTHON) verify_import.py

recovery: verify-imports
	$(PYTHON) tools/build_recovery.py

test: verify-imports
	$(PYTHON) tools/embed_recovery_web.py --check
	$(PYTHON) tests/test_recovery_core.py
	$(PYTHON) tests/test_recovery_http.py
	$(PYTHON) tests/test_recovery_native.py
	$(PYTHON) tests/test_http_server.py

web-test: verify-imports
	$(PYTHON) tests/recovery_browser.py

verify: recovery test
	$(PYTHON) verify_import.py --build

# Preserve the command names used by the archived design notes.
recovery-verify: verify

recovery-web-test: web-test

clean:
	rm -rf build
