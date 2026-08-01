import Foundation
import Testing
@testable import Kaodoku

private let local = URL(string: "http://192.168.1.10:8080")!
private let pub = URL(string: "https://kaodoku.example.com")!

private func endpoints(_ mode: ServerEndpoints.Mode,
                       local l: URL? = local, public p: URL? = pub) -> ServerEndpoints {
    ServerEndpoints(localURL: l, publicURL: p, instanceID: "abc", mode: mode)
}

@Test("Auto prefers verified local, otherwise public")
func autoSelection() {
    let e = endpoints(.auto)
    #expect(e.activeURL(localVerified: true) == local)
    #expect(e.activeURL(localVerified: false) == pub)
    #expect(e.wantsProbe)
}

@Test("Manual Local still requires verification when the pair is verifiable")
func manualLocalVerifies() {
    let e = endpoints(.local)
    #expect(e.activeURL(localVerified: true) == local)
    // Unverified local must NOT win — a foreign LAN can squat the address.
    #expect(e.activeURL(localVerified: false) == pub)
    #expect(e.wantsProbe)
}

@Test("Manual Public never probes and never picks local while public exists")
func manualPublic() {
    let e = endpoints(.external)
    #expect(!e.wantsProbe)
    #expect(e.activeURL(localVerified: false) == pub)
    #expect(e.activeURL(localVerified: true) == pub)
    #expect(endpoints(.external, public: nil).activeURL(localVerified: false) == local)
}

@Test("Single-slot and identical pairs keep plain behavior, no probe")
func unverifiable() {
    // Only one URL configured.
    #expect(!endpoints(.auto, public: nil).wantsProbe)
    #expect(endpoints(.auto, public: nil).activeURL(localVerified: false) == local)
    #expect(!endpoints(.auto, local: nil).wantsProbe)
    #expect(endpoints(.auto, local: nil).activeURL(localVerified: false) == pub)
    // Identical origins in both slots: treated as one endpoint.
    #expect(!endpoints(.auto, local: pub).wantsProbe)
}
