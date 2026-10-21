// Erzeugt ein 1024x1024-PNG als App-Symbol: Bildschirm mit Zeiger auf blauem Verlauf.
import AppKit

let size = 1024
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8,
                           samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                           bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
let rect = NSRect(x: 40, y: 40, width: 944, height: 944)
let bg = NSBezierPath(roundedRect: rect, xRadius: 210, yRadius: 210)
NSGradient(colors: [NSColor(red: 0.15, green: 0.39, blue: 0.92, alpha: 1), NSColor(red: 0.07, green: 0.16, blue: 0.45, alpha: 1)])!
    .draw(in: bg, angle: -90)
// Monitor
NSColor.white.setFill()
NSBezierPath(roundedRect: NSRect(x: 214, y: 360, width: 596, height: 396), xRadius: 38, yRadius: 38).fill()
NSColor(red: 0.07, green: 0.16, blue: 0.45, alpha: 1).setFill()
NSBezierPath(roundedRect: NSRect(x: 246, y: 392, width: 532, height: 332), xRadius: 18, yRadius: 18).fill()
NSColor.white.setFill()
NSBezierPath(rect: NSRect(x: 462, y: 296, width: 100, height: 64)).fill()
NSBezierPath(roundedRect: NSRect(x: 372, y: 262, width: 280, height: 44), xRadius: 22, yRadius: 22).fill()
// Mauszeiger
let c = NSBezierPath()
c.move(to: NSPoint(x: 560, y: 650)); c.line(to: NSPoint(x: 560, y: 440)); c.line(to: NSPoint(x: 614, y: 492))
c.line(to: NSPoint(x: 662, y: 420)); c.line(to: NSPoint(x: 700, y: 440)); c.line(to: NSPoint(x: 654, y: 514))
c.line(to: NSPoint(x: 730, y: 520)); c.close()
NSColor(red: 0.2, green: 0.83, blue: 0.5, alpha: 1).setFill(); c.fill()
NSColor.white.setStroke(); c.lineWidth = 10; c.stroke()
NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
