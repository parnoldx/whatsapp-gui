PREFIX := $(HOME)/.local

.PHONY: dependencies build install launch
dependencies:
	sudo pacman -S --needed cmake ninja go qt6-base qt6-declarative qt6-multimedia qt6-webengine

build:
	cmake -B build -DCMAKE_BUILD_TYPE=Release >/dev/null
	cmake --build build -j

install: build
	cmake --install build

launch:
	-pkill -f whatsapp-gui
	$(MAKE) install
	nohup whatsapp-gui >/dev/null 2>&1 & disown

VERSION := $(shell git describe --tags --always --dirty)

.PHONY: dist
dist: build
	rm -rf dist && mkdir -p dist/whatsapp-gui
	cmake --install build --prefix dist/whatsapp-gui >/dev/null
	cp README.md LICENSE dist/whatsapp-gui/
	tar -C dist -czf dist/whatsapp-gui-$(VERSION).tar.gz whatsapp-gui
	@echo "dist/whatsapp-gui-$(VERSION).tar.gz"
