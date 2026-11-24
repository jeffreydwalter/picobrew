PYTHON ?= python3
HUB_HOST ?= picobrew.com
.PHONY: standalone firmware-package standalone-test usb-test web-test wifi-bench bootstrap

standalone firmware-package:
	"$(PYTHON)" tools/package.py --hub-host "$(HUB_HOST)" $(if $(OEM_FILE),--oem-file "$(OEM_FILE)") $(if $(KEY_FILE),--key-file "$(KEY_FILE)") $(if $(DEVICE_UID),--uid "$(DEVICE_UID)") $(if $(OFFLINE_PROGRAM),--offline-program "$(OFFLINE_PROGRAM)")

standalone-test usb-test web-test:
	"$(PYTHON)" -m unittest discover -s tests

wifi-bench:
	"$(PYTHON)" wifi/integration/verify_native.py
	"$(PYTHON)" wifi/integration/verify_avr.py --toolchain "$(ARDUINO_AVR_BIN)" --oem-file "$(ZYMATIC_TEST_HEX)" --recipe-pause
	"$(PYTHON)" wifi/layout/network/compact.py --toolchain "$(ARDUINO_AVR_BIN)" --oem-file "$(ZYMATIC_TEST_HEX)"
	"$(PYTHON)" wifi/layout/oled/compact.py --toolchain "$(ARDUINO_AVR_BIN)" --oem-file "$(ZYMATIC_TEST_HEX)"
	"$(PYTHON)" wifi/layout/compact.py --toolchain "$(ARDUINO_AVR_BIN)" --oem-file "$(ZYMATIC_TEST_HEX)" --simulate
	"$(PYTHON)" wifi/layout/scatter.py --toolchain "$(ARDUINO_AVR_BIN)" --oem-file "$(ZYMATIC_TEST_HEX)" --with-listener

bootstrap:
	"$(PYTHON)" -m pip install pyserial==3.5
