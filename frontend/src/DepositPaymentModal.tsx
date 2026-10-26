import {Copy,Info} from 'lucide-react'
import Modal from './Modal'
import DepositQRCode from './DepositQRCode'
import DepositScanPanel from './DepositScanPanel'

export default function DepositPaymentModal({address,network,onClose,onRefresh,notify}:{address:string;network:string;onClose:()=>void;onRefresh:()=>Promise<void>;notify:(message:string)=>void}){
  const copy=async()=>{
    try{await navigator.clipboard.writeText(address);notify('地址已复制')}
    catch{notify('请长按收款地址手动复制')}
  }
  return <Modal title="USDT 充值" onClose={onClose} className="deposit-payment-modal">
    {network!=='mainnet'&&<div className="portal-warning" role="alert">当前为测试网络，请勿转入真实资金。</div>}
    <div className="deposit-payment-layout">
      <div className="deposit-payment-code"><DepositQRCode address={address}/></div>
      <div className="deposit-network-line">
        <div><span>充值网络</span><strong>TRON · TRC20</strong></div>
        <span className={`status-pill ${network==='mainnet'?'ok':'warn'}`}>{network==='mainnet'?'主网':`${network} 测试网`}</span>
      </div>
      <div className="deposit-address">
        <span>专属收款地址</span>
        <code className="portal-address" tabIndex={0} aria-label="专属收款地址" dir="ltr">{address}</code>
        <button type="button" className="button primary deposit-copy-button" onClick={()=>void copy()}><Copy size={16}/>复制收款地址</button>
      </div>
      <p className="deposit-network-note"><Info size={16}/><span>仅转入 TRC20 USDT，请核对网络与地址。</span></p>
    </div>
    <DepositScanPanel onRefresh={onRefresh}/>
  </Modal>
}
