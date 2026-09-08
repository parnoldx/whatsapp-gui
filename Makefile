PREFIX := $(HOME)/.local

.PHONY: install launch
install:
	cmake -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=$(PREFIX) >/dev/null
	cmake --build build -j
	cmake --install build

launch:
	-pkill -f whatsapp-gui
	cmake --install build
	nohup whatsapp-gui >/dev/null 2>&1 & disown
