import SwiftUI
import UIKit

/// StripReader is a UICollectionView-backed vertical reader with pinch zoom.
struct StripReader: UIViewRepresentable {
  struct Jump: Equatable {
    var id: Int
    var index: Int
  }

  let pages: [ReaderView.PageRef]
  let startIndex: Int
  var enhanced = false
  let maxPixelSize: CGSize
  let estimateAspect: CGFloat
  var jump: Jump?
  var comfort = ComfortParams(warmth: 0, dim: 0, whiteOnly: false)
  let loadImage: (ReaderView.PageRef, CGSize) async -> UIImage?
  let onPage: (Int) -> Void
  let onFinished: (Int) -> Void

  func makeCoordinator() -> Coordinator {
    Coordinator(self)
  }

  func makeUIView(context: Context) -> ZoomStripView {
    let v = ZoomStripView()
    v.collectionView.dataSource = context.coordinator
    v.collectionView.register(StripCell.self, forCellWithReuseIdentifier: StripCell.id)
    context.coordinator.view = v
    v.onLayout = { [weak coordinator = context.coordinator] in coordinator?.resumeIfNeeded() }
    v.onScroll = { [weak coordinator = context.coordinator] in coordinator?.reportPage() }
    return v
  }

  func updateUIView(_: ZoomStripView, context: Context) {
    context.coordinator.apply(self)
  }

  /// aspect returns width/height for a page.
  static func aspect(_ ref: ReaderView.PageRef, estimate: CGFloat) -> CGFloat {
    if ref.transition {
      return 3
    }
    let key = "\(ref.volume ? "v" : "c")\(ref.chapterID)-\(ref.page)"
    return max(ReaderView.aspects[key] ?? estimate, 0.05)
  }

  final class Coordinator: NSObject, UICollectionViewDataSource {
    private var parent: StripReader
    private var pages: [ReaderView.PageRef]
    weak var view: ZoomStripView?
    private var didResume = false
    private var reported = -1
    private var finished = -1
    private var jumping = false
    private var lastJumpID = 0
    private var enhanced = false
    private var comfort: ComfortParams

    init(_ parent: StripReader) {
      self.parent = parent
      pages = parent.pages
      comfort = parent.comfort
    }

    func apply(_ parent: StripReader) {
      self.parent = parent
      guard let v = view else { return }
      if parent.enhanced != enhanced {
        enhanced = parent.enhanced
        v.collectionView.reloadData()
      }
      if parent.comfort != comfort {
        comfort = parent.comfort
        for case let cell as StripCell in v.collectionView.visibleCells {
          cell.setComfort(comfort)
        }
      }
      let newPages = parent.pages
      if newPages.count != pages.count {
        let grew = newPages.count > pages.count
          && Array(newPages.prefix(pages.count)) == pages
        let old = pages.count
        pages = newPages
        syncAspects()
        if grew, didResume {
          v.collectionView.performBatchUpdates {
            v.collectionView.insertItems(at: (old ..< newPages.count).map { IndexPath(item: $0, section: 0) })
          }
        } else {
          v.collectionView.reloadData()
        }
        v.adjustContentSize()
      } else {
        pages = newPages
        syncAspects()
      }
      resumeIfNeeded()
      if let j = parent.jump, j.id != lastJumpID, didResume, pages.indices.contains(j.index) {
        lastJumpID = j.id
        reported = j.index
        finished = j.index - 1
        jumping = true
        v.scrollToItem(j.index)
        jumping = false
        let onPage = parent.onPage
        DispatchQueue.main.async { onPage(j.index) }
      }
    }

    private func syncAspects() {
      view?.layout.aspects = pages.map { StripReader.aspect($0, estimate: parent.estimateAspect) }
    }

    func resumeIfNeeded() {
      guard !didResume, let v = view, !pages.isEmpty, v.bounds.width > 0 else { return }
      let i = min(max(parent.startIndex, 0), pages.count - 1)
      v.scrollToItem(i)
      didResume = true
      reported = i
      finished = i - 1
    }

    func reportPage() {
      guard didResume, !jumping, let v = view else { return }
      if let f = v.finishedItem(), f > finished {
        for j in (finished + 1) ... f where pages.indices.contains(j) {
          parent.onFinished(j)
        }
        finished = f
      }
      guard let i = v.topItem(), i != reported else { return }
      reported = i
      parent.onPage(i)
    }

    func collectionView(_: UICollectionView, numberOfItemsInSection _: Int) -> Int {
      pages.count
    }

    func collectionView(_ cv: UICollectionView, cellForItemAt indexPath: IndexPath) -> UICollectionViewCell {
      guard let cell = cv.dequeueReusableCell(withReuseIdentifier: StripCell.id, for: indexPath) as? StripCell else {
        return UICollectionViewCell()
      }
      let page = pages[indexPath.item]
      let loader = parent.loadImage
      cell.setComfort(comfort)
      cell.load(page, size: parent.maxPixelSize) { await loader($0, $1) }
      return cell
    }
  }
}

/// ZoomStripView pairs the rendering collection view with the gesture-owning
/// overlay scroll view.
final class ZoomStripView: UIView, UIScrollViewDelegate {
  let layout = ZoomStripLayout()
  private(set) lazy var collectionView = UICollectionView(frame: .zero, collectionViewLayout: layout)
  private let overlay = UIScrollView()
  private let dummy = UIView()
  var onLayout: (() -> Void)?
  var onScroll: (() -> Void)?

  override init(frame: CGRect) {
    super.init(frame: frame)
    collectionView.backgroundColor = .black
    collectionView.isUserInteractionEnabled = false // overlay owns all touches
    collectionView.contentInsetAdjustmentBehavior = .never
    overlay.delegate = self
    overlay.applyReaderZoomDefaults()
    overlay.bouncesZoom = false // zoom bounce doesn't emit scrollViewDidZoom
    overlay.addSubview(dummy)
    addSubview(collectionView)
    addSubview(overlay)
  }

  @available(*, unavailable)
  required init?(coder _: NSCoder) {
    fatalError()
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    collectionView.frame = bounds
    overlay.frame = bounds
    adjustContentSize()
    onLayout?()
  }

  func adjustContentSize() {
    collectionView.layoutIfNeeded()
    let size = layout.collectionViewContentSize
    if overlay.contentSize != size {
      overlay.contentSize = size
      dummy.frame = CGRect(origin: .zero, size: size)
    }
  }

  func scrollToItem(_ i: Int) {
    adjustContentSize()
    guard let f = collectionView.layoutAttributesForItem(at: IndexPath(item: i, section: 0))?.frame else { return }
    let maxY = max(overlay.contentSize.height - bounds.height, 0)
    overlay.setContentOffset(CGPoint(x: 0, y: min(f.minY, maxY)), animated: false)
    collectionView.contentOffset = overlay.contentOffset
  }

  func topItem() -> Int? {
    collectionView.indexPathForItem(at: CGPoint(x: overlay.contentOffset.x + 1,
                                                y: overlay.contentOffset.y + 1))?.item
  }

  /// finishedItem returns the last item whose bottom edge has scrolled into view.
  func finishedItem() -> Int? {
    guard layout.preparedScale == overlay.zoomScale else { return nil }
    let count = collectionView.numberOfItems(inSection: 0)
    guard count > 0 else { return nil }
    let bottomY = overlay.contentOffset.y + bounds.height
    if bottomY >= layout.collectionViewContentSize.height - 0.5 {
      return count - 1
    }
    guard let item = collectionView.indexPathForItem(at: CGPoint(x: overlay.contentOffset.x + 1,
                                                                 y: bottomY))?.item,
      item > 0 else { return nil }
    return item - 1
  }

  func scrollViewDidScroll(_ s: UIScrollView) {
    collectionView.contentOffset = s.contentOffset
    onScroll?()
  }

  func scrollViewDidZoom(_ s: UIScrollView) {
    guard layout.scale != s.zoomScale else { return }
    layout.scale = s.zoomScale
    layout.invalidateLayout()
  }

  func viewForZooming(in _: UIScrollView) -> UIView? {
    dummy
  }
}

final class ZoomStripLayout: UICollectionViewLayout {
  var scale: CGFloat = 1
  var aspects: [CGFloat] = [] {
    didSet {
      if aspects != oldValue {
        invalidateLayout()
      }
    }
  }

  private var frames: [CGRect] = []
  private var size: CGSize = .zero
  private var lastWidth: CGFloat = 0
  private(set) var preparedScale: CGFloat = 1

  override func prepare() {
    super.prepare()
    guard let cv = collectionView else { return }
    lastWidth = cv.bounds.width
    preparedScale = scale
    let px = cv.traitCollection.displayScale > 0 ? cv.traitCollection.displayScale : 1
    let width = cv.bounds.width * scale
    frames.removeAll(keepingCapacity: true)
    frames.reserveCapacity(aspects.count)
    var y: CGFloat = 0
    var exact: CGFloat = 0
    for a in aspects {
      exact += width / a
      let next = (exact * px).rounded() / px
      frames.append(CGRect(x: 0, y: y, width: width, height: next - y))
      y = next
    }
    size = CGSize(width: width, height: y)
  }

  override var collectionViewContentSize: CGSize {
    size
  }

  override func layoutAttributesForElements(in rect: CGRect) -> [UICollectionViewLayoutAttributes]? {
    guard !frames.isEmpty else { return [] }
    var lo = 0, hi = frames.count - 1, start = frames.count
    while lo <= hi {
      let mid = (lo + hi) / 2
      if frames[mid].maxY > rect.minY {
        start = mid
        hi = mid - 1
      } else {
        lo = mid + 1
      }
    }
    var out: [UICollectionViewLayoutAttributes] = []
    var i = start
    while i < frames.count, frames[i].minY < rect.maxY {
      out.append(attributes(i))
      i += 1
    }
    return out
  }

  override func layoutAttributesForItem(at indexPath: IndexPath) -> UICollectionViewLayoutAttributes? {
    frames.indices.contains(indexPath.item) ? attributes(indexPath.item) : nil
  }

  override func shouldInvalidateLayout(forBoundsChange newBounds: CGRect) -> Bool {
    newBounds.width != lastWidth
  }

  private func attributes(_ i: Int) -> UICollectionViewLayoutAttributes {
    let a = UICollectionViewLayoutAttributes(forCellWith: IndexPath(item: i, section: 0))
    a.frame = frames[i]
    return a
  }
}

final class StripCell: UICollectionViewCell {
  static let id = "strip"
  private let imageView = UIImageView()
  private let label = UILabel()
  private var task: Task<Void, Never>?
  private var pageKey: ReaderView.PageRef?
  private var original: UIImage? // pre-filter, so comfort can re-apply live
  private var comfort = ComfortParams(warmth: 0, dim: 0, whiteOnly: false)

  override init(frame: CGRect) {
    super.init(frame: frame)
    imageView.contentMode = .scaleToFill
    imageView.backgroundColor = .black
    imageView.frame = contentView.bounds
    imageView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
    contentView.addSubview(imageView)
    label.textColor = .secondaryLabel
    label.font = .preferredFont(forTextStyle: .callout)
    label.textAlignment = .center
    label.adjustsFontSizeToFitWidth = true
    label.frame = contentView.bounds.insetBy(dx: 16, dy: 0)
    label.autoresizingMask = [.flexibleWidth, .flexibleHeight]
    label.isHidden = true
    contentView.addSubview(label)
  }

  @available(*, unavailable)
  required init?(coder _: NSCoder) {
    fatalError()
  }

  func load(_ page: ReaderView.PageRef, size: CGSize,
            using loader: @escaping (ReaderView.PageRef, CGSize) async -> UIImage?)
  {
    guard page != pageKey else { return }
    pageKey = page
    task?.cancel()
    imageView.image = nil
    label.isHidden = !page.transition
    imageView.isHidden = page.transition
    if page.transition {
      label.text = page.label
      return
    }
    task = Task { @MainActor in
      let img = await loader(page, size)
      guard !Task.isCancelled else { return }
      original = img
      imageView.image = img.map { ReaderComfort.apply($0, comfort) }
    }
  }

  /// setComfort re-tints the already-loaded page without a reload.
  func setComfort(_ p: ComfortParams) {
    guard p != comfort else { return }
    comfort = p
    if let original {
      imageView.image = ReaderComfort.apply(original, p)
    }
  }

  override func prepareForReuse() {
    super.prepareForReuse()
    task?.cancel()
    task = nil
    pageKey = nil
    original = nil
    imageView.image = nil
  }
}
