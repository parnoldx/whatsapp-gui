cmake_minimum_required(VERSION 3.20)
project(whatsapp-gui LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTORCC ON)

find_package(Qt6 REQUIRED COMPONENTS Core Gui Network Qml Quick QuickControls2 Multimedia WebEngineQuick)

qt_add_executable(whatsapp-gui
    src/main.cpp
    src/OmarchyTheme.hpp src/OmarchyTheme.cpp
    src/WhatsApp.hpp src/WhatsApp.cpp
)

qt_add_resources(whatsapp-gui "qml"
    PREFIX "/"
    FILES
        qml/Main.qml
        qml/AppButton.qml
        qml/Avatar.qml
        qml/Composer.qml
        qml/MessageDelegate.qml
        qml/VoiceRecorder.qml
        qml/Model.js
)

target_include_directories(whatsapp-gui PRIVATE src)
target_link_libraries(whatsapp-gui PRIVATE
    Qt6::Core Qt6::Gui Qt6::Network Qt6::Qml Qt6::Quick Qt6::QuickControls2 Qt6::Multimedia Qt6::WebEngineQuick)

# The Go backend: the helper binary embedding wacli (vendored under wacli/),
# plus the wacli CLI itself, needed for the initial `wacli auth` pairing.
file(GLOB_RECURSE GO_SOURCES ${CMAKE_SOURCE_DIR}/wacli/*.go)

set(HELPER_BIN ${CMAKE_BINARY_DIR}/helper/whatsapp)
add_custom_command(
	OUTPUT ${HELPER_BIN}
	COMMAND ${CMAKE_COMMAND} -E env GOFLAGS=-mod=mod
	        go build -tags sqlite_fts5 -o ${HELPER_BIN}
	        ./cmd/whatsapp-helper
	WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}/wacli
	DEPENDS ${GO_SOURCES}
	COMMENT "Building the Go helper (wacli embedded)"
	VERBATIM)
add_custom_target(helper ALL DEPENDS ${HELPER_BIN})

set(WACLI_BIN ${CMAKE_BINARY_DIR}/helper/wacli)
add_custom_command(
	OUTPUT ${WACLI_BIN}
	COMMAND ${CMAKE_COMMAND} -E env GOFLAGS=-mod=mod
	        go build -tags sqlite_fts5 -o ${WACLI_BIN}
	        ./cmd/wacli
	WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}/wacli
	DEPENDS ${GO_SOURCES}
	COMMENT "Building the wacli CLI"
	VERBATIM)
add_custom_target(wacli-bin ALL DEPENDS ${WACLI_BIN})

include(GNUInstallDirs)
install(TARGETS whatsapp-gui RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(PROGRAMS ${CMAKE_BINARY_DIR}/helper/wacli DESTINATION ${CMAKE_INSTALL_BINDIR})
install(PROGRAMS ${CMAKE_BINARY_DIR}/helper/whatsapp DESTINATION ${CMAKE_INSTALL_LIBDIR}/whatsapp-gui
        RENAME whatsapp)
install(FILES share/applications/whatsapp-gui.desktop
        DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/applications)
install(FILES share/icons/hicolor/scalable/apps/whatsapp-gui.svg
        DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/icons/hicolor/scalable/apps)

# The daemon-pipe framing test drives the real WhatsApp class against a stub
# helper (tests/stub-helper.sh), so it needs no wacli store and no network.
find_package(Qt6 COMPONENTS Test QUIET)
if(Qt6Test_FOUND)
    enable_testing()
    add_executable(pipe-test tests/pipe_test.cpp src/WhatsApp.hpp src/WhatsApp.cpp)
    target_include_directories(pipe-test PRIVATE src)
    target_compile_definitions(pipe-test PRIVATE STUB_HELPER="${CMAKE_CURRENT_SOURCE_DIR}/tests/stub-helper.sh")
    target_link_libraries(pipe-test PRIVATE Qt6::Core Qt6::Gui Qt6::Qml Qt6::Test)
    add_test(NAME pipe COMMAND pipe-test)
    set_tests_properties(pipe PROPERTIES ENVIRONMENT "QT_QPA_PLATFORM=offscreen" TIMEOUT 30)
endif()
