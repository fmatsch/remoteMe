// Zeichnet den Inhalt der ersten Eingabezeile als QR-Code ins Terminal (ohne Dateien).
import CoreImage
import Foundation

guard let text = readLine(), let filter = CIFilter(name: "CIQRCodeGenerator") else { exit(1) }
filter.setValue(Data(text.utf8), forKey: "inputMessage")
filter.setValue("M", forKey: "inputCorrectionLevel")
guard let image = filter.outputImage else { exit(1) }
let w = Int(image.extent.width), h = Int(image.extent.height)
var px = [UInt8](repeating: 255, count: w * h * 4)
CIContext().render(image, toBitmap: &px, rowBytes: w * 4, bounds: image.extent, format: .RGBA8, colorSpace: CGColorSpaceCreateDeviceRGB())

let q = 3 // weißer Rand
func dark(_ x: Int, _ y: Int) -> Bool { x >= 0 && y >= 0 && x < w && y < h && px[(y * w + x) * 4] < 128 }
var y = -q
while y < h + q {
    var line = ""
    for x in -q..<(w + q) {
        let top = dark(x, y) ? 16 : 231, bottom = dark(x, y + 1) ? 16 : 231
        line += "\u{1b}[38;5;\(top);48;5;\(bottom)m▀"
    }
    print(line + "\u{1b}[0m")
    y += 2
}
