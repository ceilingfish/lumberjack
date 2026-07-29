import Testing
@testable import LumberjackMenuBar

/// The slot the status item claims on first launch. The rule that matters is
/// that the notch bound always wins: a slot under the notch is invisible, which
/// is the very bug the autosaveName change exists to fix.
@MainActor
struct MenuBarSlotTests {
    @Test("a display with no notch keeps the preferred slot")
    func noNotchUsesPreferred() {
        #expect(AppDelegate.menuBarSlot(rightOfNotch: nil) == AppDelegate.preferredMenuBarPosition)
    }

    @Test("a strip roomier than we need keeps the preferred slot")
    func roomyStripUsesPreferred() {
        // 14"/16" MBP at default scaling: ~656pt right of the notch, which
        // leaves more than the 640 we want even after the width allowance.
        #expect(AppDelegate.menuBarSlot(rightOfNotch: 900) == AppDelegate.preferredMenuBarPosition)
    }

    @Test("a narrow strip pulls the slot in to clear the notch")
    func narrowStripClearsTheNotch() {
        // A 13" Air at ~1280pt logical width: the notch starts ~550pt from the
        // right edge, so the whole item has to fit inside that.
        let slot = AppDelegate.menuBarSlot(rightOfNotch: 550)
        #expect(slot == 550 - AppDelegate.statusItemWidthAllowance)
        #expect(slot < 550, "the item must sit entirely clear of the notch")
    }

    @Test("the notch bound wins even when it crowds the system items")
    func notchBoundBeatsTheSystemItems() {
        // Pathologically narrow: we would rather overlap a neighbour, which is
        // visible and one drag fixes, than hide under the notch.
        #expect(AppDelegate.menuBarSlot(rightOfNotch: 200) == 200 - AppDelegate.statusItemWidthAllowance)
    }

    @Test("the whole item clears the notch, not just its right-hand edge")
    func allowsForTheItemsOwnWidth() {
        #expect(AppDelegate.menuBarSlot(rightOfNotch: 400) < 400)
    }
}
