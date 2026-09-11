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

# The Go backend: a single binary embedding wacli (vendored under wacli/).
file(GLOB_RECURSE HELPER_SOURCES ${CMAKE_SOURCE_DIR}/wacli/*.go)
set(HELPER_BIN ${CMAKE_BINARY_DIR}/helper/whatsapp)
add_custom_command(
    OUTPUT ${HELPER_BIN}
    COMMAND ${CMAKE_COMMAND} -E env GOFLAGS=-mod=mod
            go build -tags sqlite_fts5 -o ${HELPER_BIN}
            ./cmd/whatsapp-helper
    WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}/wacli
    DEPENDS ${HELPER_SOURCES}
    COMMENT "Building the Go helper (wacli embedded)"
    VERBATIM)
add_custom_target(helper ALL DEPENDS ${HELPER_BIN})

include(GNUInstallDirs)
install(TARGETS whatsapp-gui RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(PROGRAMS ${HELPER_BIN} DESTINATION ${CMAKE_INSTALL_LIBDIR}/whatsapp-gui
        RENAME whatsapp)
install(FILES share/applications/whatsapp-gui.desktop
        DESTINATION ${CMAKE_INSTALL_DATAROOTDIR}/applications)
