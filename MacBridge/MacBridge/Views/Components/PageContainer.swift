import SwiftUI

struct PageContainer<Content: View>: View {
    private let scrolls: Bool
    private let maxContentWidth: CGFloat
    private let content: Content

    /// - Parameters:
    ///   - scrolls: 内容是否包裹在 ScrollView 中。
    ///   - maxContentWidth: 内容列最大宽度（包含两侧 pageHorizontalPadding）。
    ///     Workspace 宽屏时传入 workspaceMaxContainerWidth（1224/1264）。
    ///     默认取 workColumnWidth（880pt）。
    init(scrolls: Bool = true, maxContentWidth: CGFloat = LayoutConstants.workColumnWidth, @ViewBuilder content: () -> Content) {
        self.scrolls = scrolls
        self.maxContentWidth = maxContentWidth
        self.content = content()
    }

    var body: some View {
        Group {
            if scrolls {
                ScrollView {
                    pageContent
                }
            } else {
                pageContent
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var pageContent: some View {
        content
            .frame(maxWidth: .infinity, maxHeight: scrolls ? nil : .infinity, alignment: .topLeading)
            .padding(.horizontal, LayoutConstants.pageHorizontalPadding)
            .padding(.top, 26)
            .padding(.bottom, 36)
            .frame(maxWidth: maxContentWidth, alignment: .top)
    }
}

struct PageHeader<Actions: View>: View {
    let title: String
    let subtitle: String?
    private let actions: Actions

    init(
        _ title: String,
        subtitle: String? = nil,
        @ViewBuilder actions: () -> Actions
    ) {
        self.title = title
        self.subtitle = subtitle
        self.actions = actions()
    }

    var body: some View {
        HStack(alignment: .top, spacing: 16) {
            VStack(alignment: .leading, spacing: 5) {
                Text(title)
                    .font(.title2.weight(.semibold))
                if let subtitle {
                    Text(subtitle)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }

            Spacer(minLength: 16)
            actions
        }
    }
}

extension PageHeader where Actions == EmptyView {
    init(_ title: String, subtitle: String? = nil) {
        self.init(title, subtitle: subtitle) {
            EmptyView()
        }
    }
}
