import SwiftUI

// Theme is the web UI's nord theme (dark), matching app.css [data-theme=nord].
enum Theme {
    static let bg = Color(hex: 0x2E3440)       // base-200 page background
    static let surface = Color(hex: 0x3B4252)  // base-100 cards/rows
    static let neutral = Color(hex: 0x434C5E)
    static let line = Color(hex: 0x4C566A)
    static let mutedText = Color(hex: 0x9AA5BC)
    static let primary = Color(hex: 0x88C0D0)
    static let info = Color(hex: 0x5E81AC)
    static let success = Color(hex: 0xA3BE8C)
    static let warning = Color(hex: 0xEBCB8B)
    static let error = Color(hex: 0xBF616A)
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255,
                  green: Double((hex >> 8) & 0xFF) / 255,
                  blue: Double(hex & 0xFF) / 255)
    }
}

extension View {
    // nordScreen paints a screen with the nord base background.
    func nordScreen() -> some View {
        scrollContentBackground(.hidden).background(Theme.bg.ignoresSafeArea())
    }

    // nordRows gives List/Form rows the nord surface color.
    func nordRows() -> some View {
        listRowBackground(Theme.surface)
    }
}
