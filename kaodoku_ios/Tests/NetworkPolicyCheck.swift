import Foundation
import Testing
@testable import Kaodoku

@Test("Private and public hosts are classified exactly")
func hostClassification() {
    for host in [
        "localhost", "reader.local", "reader", "127.0.0.1", "10.0.0.1",
        "172.31.0.1", "192.168.1.1", "169.254.1.1", "fd00::1", "fe80::1",
    ] {
        #expect(isPrivateHost(host), "\(host) should be private")
    }
    for host in [
        "example.com", "10.0.0.1.evil.example", "8.8.8.8", "172.32.0.1",
        "192.169.1.1", "2001:4860:4860::8888",
    ] {
        #expect(!isPrivateHost(host), "\(host) should be public")
    }
}

@Test("Only HTTPS or private HTTP server URLs are allowed")
func serverURLPolicy() {
    #expect(isAllowedServerURL(URL(string: "https://example.com")!))
    #expect(isAllowedServerURL(URL(string: "http://[fd00::1]:8080")!))
    #expect(!isAllowedServerURL(URL(string: "http://example.com")!))
    #expect(!isAllowedServerURL(URL(string: "ftp://192.168.1.1")!))
}

@Test("Redirects keep API keys only on the same allowed origin")
func redirectPolicy() {
    let source = URL(string: "https://reader.example/api")!
    let response = HTTPURLResponse(url: source, statusCode: 302, httpVersion: nil, headerFields: nil)!

    func redirected(to destination: String) -> URLRequest? {
        let task = URLSession.shared.dataTask(with: source)
        defer { task.cancel() }
        var request = URLRequest(url: URL(string: destination)!)
        request.setValue("secret", forHTTPHeaderField: "X-API-Key")
        var result: URLRequest?
        RedirectGuard().urlSession(
            URLSession.shared, task: task, willPerformHTTPRedirection: response,
            newRequest: request
        ) { result = $0 }
        return result
    }

    #expect(redirected(to: "https://reader.example:443/next")?.value(
        forHTTPHeaderField: "X-API-Key") == "secret")
    #expect(redirected(to: "https://other.example/next")?.value(
        forHTTPHeaderField: "X-API-Key") == nil)
    #expect(redirected(to: "https://reader.example:8443/next")?.value(
        forHTTPHeaderField: "X-API-Key") == nil)
    #expect(redirected(to: "http://example.com/next") == nil)
}
