import SwiftUI
import UIKit

// StripReader is a UICollectionView-backed vertical reader.
// A flow layout computes every page's height from its known aspect up front.
struct StripReader: UIViewRepresentable {
    let pages: [ReaderView.PageRef]
    let startIndex: Int
    let maxPixelSize: CGSize
    let estimateAspect: CGFloat
    let loadImage: (ReaderView.PageRef, CGSize) async -> UIImage?
    let onPage: (Int) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> UICollectionView {
        let layout = UICollectionViewFlowLayout()
        layout.scrollDirection = .vertical
        layout.minimumLineSpacing = 0
        layout.minimumInteritemSpacing = 0
        layout.sectionInset = .zero

        let cv = LayoutReportingCollectionView(frame: .zero, collectionViewLayout: layout)
        cv.backgroundColor = .black
        cv.showsVerticalScrollIndicator = false
        cv.contentInsetAdjustmentBehavior = .never
        cv.dataSource = context.coordinator
        cv.delegate = context.coordinator
        cv.register(StripCell.self, forCellWithReuseIdentifier: StripCell.id)
        cv.onLayout = { [weak coordinator = context.coordinator] in coordinator?.resumeIfNeeded() }
        context.coordinator.collectionView = cv

        return cv
    }

    func updateUIView(_ cv: UICollectionView, context: Context) {
        context.coordinator.apply(self)
    }

    // aspect returns width/height for a page.
    static func aspect(_ ref: ReaderView.PageRef, estimate: CGFloat) -> CGFloat {
        let key = "\(ref.volume ? "v" : "c")\(ref.chapterID)-\(ref.page)"
        return max(ReaderView.aspects[key] ?? estimate, 0.05)
    }

    final class Coordinator: NSObject, UICollectionViewDataSource, UICollectionViewDelegateFlowLayout {
        private var parent: StripReader
        private var pages: [ReaderView.PageRef]
        weak var collectionView: UICollectionView?
        private var didResume = false
        private var reported = -1

        init(_ parent: StripReader) {
            self.parent = parent
            self.pages = parent.pages
        }

        func apply(_ parent: StripReader) {
            self.parent = parent
            guard let cv = collectionView else { return }
            let newPages = parent.pages
            if newPages.count != pages.count {
                let grew = newPages.count > pages.count
                    && Array(newPages.prefix(pages.count)) == pages
                let old = pages.count
                pages = newPages
                if grew && didResume {
                    cv.performBatchUpdates {
                        cv.insertItems(at: (old..<newPages.count).map { IndexPath(item: $0, section: 0) })
                    }
                } else {
                    cv.reloadData()
                }
            } else {
                pages = newPages
            }
            resumeIfNeeded()
        }

        // resumeIfNeeded jumps to the resume page once, when the collection view
        // has a real width — running it at zero width would land nowhere (black).
        func resumeIfNeeded() {
            guard !didResume, let cv = collectionView, !pages.isEmpty, cv.bounds.width > 0 else { return }
            cv.layoutIfNeeded()
            let i = min(max(parent.startIndex, 0), pages.count - 1)
            cv.scrollToItem(at: IndexPath(item: i, section: 0), at: .top, animated: false)
            didResume = true
            reported = i
        }

        func collectionView(_ cv: UICollectionView, numberOfItemsInSection section: Int) -> Int { pages.count }

        func collectionView(_ cv: UICollectionView, cellForItemAt indexPath: IndexPath) -> UICollectionViewCell {
            let cell = cv.dequeueReusableCell(withReuseIdentifier: StripCell.id, for: indexPath) as! StripCell
            let page = pages[indexPath.item]
            let loader = parent.loadImage
            cell.load(page, size: parent.maxPixelSize) { await loader($0, $1) }
            return cell
        }

        func collectionView(_ cv: UICollectionView, layout: UICollectionViewLayout,
                             sizeForItemAt indexPath: IndexPath) -> CGSize {
            let w = cv.bounds.width
            let scale = cv.traitCollection.displayScale > 0 ? cv.traitCollection.displayScale : 1
            let h = (w / StripReader.aspect(pages[indexPath.item], estimate: parent.estimateAspect) * scale).rounded() / scale
            return CGSize(width: w, height: h)
        }

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard let cv = collectionView, !pages.isEmpty else { return }
            let point = CGPoint(x: cv.bounds.midX, y: cv.contentOffset.y + 1)
            guard let ip = cv.indexPathForItem(at: point), ip.item != reported else { return }
            reported = ip.item
            parent.onPage(ip.item)
        }
    }
}

// LayoutReportingCollectionView reports each layout pass so the resume jump can
// fire the moment the view first has a real size (updateUIView may not re-run).
final class LayoutReportingCollectionView: UICollectionView {
    var onLayout: (() -> Void)?
    override func layoutSubviews() {
        super.layoutSubviews()
        onLayout?()
    }
}

final class StripCell: UICollectionViewCell {
    static let id = "strip"
    private let imageView = UIImageView()
    private var task: Task<Void, Never>?

    override init(frame: CGRect) {
        super.init(frame: frame)
        imageView.contentMode = .scaleToFill
        imageView.backgroundColor = .black
        imageView.frame = contentView.bounds
        imageView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        contentView.addSubview(imageView)
    }

    required init?(coder: NSCoder) { fatalError() }

    func load(_ page: ReaderView.PageRef, size: CGSize,
              using loader: @escaping (ReaderView.PageRef, CGSize) async -> UIImage?) {
        task?.cancel()
        imageView.image = nil
        task = Task { @MainActor in
            let img = await loader(page, size)
            guard !Task.isCancelled else { return }
            imageView.image = img
        }
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        task?.cancel()
        task = nil
        imageView.image = nil
    }
}
