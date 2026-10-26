import {QRCodeSVG} from 'qrcode.react'

// Encode only this customer's deposit address, never a token contract or a URL.
// SVG generation is local: no external QR service receives the address.
export default function DepositQRCode({address}:{address:string}) {
  if (!address) return null
  return <div className="deposit-qr">
    <div className="deposit-qr-code">
      <QRCodeSVG value={address} size={224} level="M" marginSize={4}
        bgColor="#ffffff" fgColor="#000000" role="img"
        aria-label="USDT TRC20 专属收款二维码" title={`收款地址：${address}`}/>
    </div>
    <span className="deposit-qr-caption">扫码转入 USDT</span>
  </div>
}
