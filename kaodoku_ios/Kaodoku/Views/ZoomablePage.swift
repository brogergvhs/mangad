import SwiftUI
import UIKit

/// ZoomablePage renders one paged display unit inside a native UIScrollView pinch-zoom.
struct ZoomablePage: UIViewRepresentable {
  struct Slot: Equatable {
    let image: UIImage
    let contentsRect: CGRect // (0,0,1,1) full, or a half of a wide page
  }

  let slots: [Slot] // in visual left-to-right order
  @Binding var zoom: CGFloat

  func makeUIView(context _: Context) -> ZoomPageUIView {
    ZoomPageUIView()
  }

  func updateUIView(_ v: ZoomPageUIView, context _: Context) {
    v.onZoomEnd = { zoom = $0 }
    v.set(slots: slots)
    v.applyZoom(zoom)
  }
}

final class ZoomPageUIView: UIView, UIScrollViewDelegate {
  private let scroll = UIScrollView()
  private let content = UIView()
  private var imageViews: [UIImageView] = []
  private var aspects: [CGFloat] = []
  private var current: [ZoomablePage.Slot] = []
  var onZoomEnd: ((CGFloat) -> Void)?

  override init(frame: CGRect) {
    super.init(frame: frame)
    scroll.delegate = self
    scroll.applyReaderZoomDefaults()
    scroll.bounces = false // at the edge, hand drags back to the pager
    scroll.addSubview(content)
    addSubview(scroll)
  }

  @available(*, unavailable)
  required init?(coder _: NSCoder) {
    fatalError()
  }

  func set(slots: [ZoomablePage.Slot]) {
    guard slots != current else { return }
    current = slots
    imageViews.forEach { $0.removeFromSuperview() }
    imageViews = slots.map { slot in
      let iv = UIImageView(image: slot.image)
      iv.contentMode = .scaleToFill
      iv.layer.contentsRect = slot.contentsRect
      content.addSubview(iv)
      return iv
    }
    aspects = slots.map {
      $0.image.size.width / max($0.image.size.height, 1) * $0.contentsRect.width
    }
    scroll.zoomScale = 1
    layoutContent()
  }

  func applyZoom(_ z: CGFloat) {
    guard !scroll.isZooming, abs(scroll.zoomScale - z) > 0.01 else { return }
    scroll.zoomScale = z
    centerContent()
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    scroll.frame = bounds
    layoutContent()
  }

  /// layoutContent fits the combined slots into the view at zoom 1, then
  /// restores the current zoom on top.
  private func layoutContent() {
    let combined = aspects.reduce(0, +)
    guard combined > 0, bounds.width > 0, bounds.height > 0 else { return }
    let h = min(bounds.height, bounds.width / combined)
    let base = CGSize(width: combined * h, height: h)
    if content.bounds.size != base {
      let z = scroll.zoomScale
      scroll.zoomScale = 1
      content.frame = CGRect(origin: .zero, size: base)
      var x: CGFloat = 0
      for (iv, a) in zip(imageViews, aspects) {
        iv.frame = CGRect(x: x, y: 0, width: a * h, height: h)
        x += a * h
      }
      scroll.contentSize = base
      scroll.zoomScale = z
    }
    centerContent()
  }

  private func centerContent() {
    let dx = max((bounds.width - scroll.contentSize.width) / 2, 0)
    let dy = max((bounds.height - scroll.contentSize.height) / 2, 0)
    scroll.contentInset = UIEdgeInsets(top: dy, left: dx, bottom: dy, right: dx)
  }

  func viewForZooming(in _: UIScrollView) -> UIView? {
    content
  }

  func scrollViewDidZoom(_: UIScrollView) {
    centerContent()
  }

  func scrollViewDidEndZooming(_: UIScrollView, with _: UIView?, atScale scale: CGFloat) {
    onZoomEnd?(scale)
  }
}

extension UIScrollView {
  func applyReaderZoomDefaults() {
    minimumZoomScale = 1
    maximumZoomScale = 3
    showsVerticalScrollIndicator = false
    showsHorizontalScrollIndicator = false
    contentInsetAdjustmentBehavior = .never
  }
}
