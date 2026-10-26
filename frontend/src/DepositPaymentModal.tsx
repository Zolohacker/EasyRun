import {Copy,ShieldCheck} from 'lucide-react'
import Modal from './Modal'
import DepositQRCode from './DepositQRCode'
import DepositScanPanel from './DepositScanPanel'

export default function DepositPaymentModal({address,network,onClose,onRefresh,notify}:{address:string;network:string;onClose:()=>void;onRefresh:()=>Promise<void>;notify:(message:string)=>void}){
  const copy=async()=>{
    try{await navigator.clipboard.writeText(address);notify('地址已复制')}
    catch{notify('请长按收款地址手动复制')}
  }
  return <Modal title="USDT 充值" onClose={onClose} className="deposit-payment-modal">
    <div className="deposit-network-line"><span className={`status-pill ${network==='mainnet'?'ok':'warn'}`}>{network==='mainnet'?'TRON 主网':`${network} 测试网`}</span><span>USDT · TRC20</span></div>
    {network!=='mainnet'&&<div className="portal-warning" role="alert">当前为测试网络，请勿转入真实资金。</div>}
    <div className="deposit-payment-layout">
      <div className="deposit-payment-code"><DepositQRCode address={address}/></div>
      <div className="deposit-payment-details">
        <div className="deposit-address"><span>本账号专属收款地址</span><code className="portal-address" tabIndex={0}>{address}</code></div>
        <button type="button" className="button subtle" onClick={()=>void copy()}><Copy size={16}/>复制收款地址</button>
        <p className="deposit-network-note"><ShieldCheck size={17}/><span>仅支持以上网络的 USDT，请确认转账网络与地址一致。</span></p>
      </div>
    </div>
    <DepositScanPanel onRefresh={onRefresh}/>
  </Modal>
}
