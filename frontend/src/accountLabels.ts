import type {Account} from './types'

export function isOpaqueWireValue(value?:string) { const text=(value || '').trim(); return text.length>=20 && text.length%4===0 && /^[A-Za-z0-9+/]+={0,2}$/.test(text) && /[+/=]/.test(text) }
export function accountDisplayName(account:Account) { const name=(account.name || '').trim(); return name && name!==account.login_name && !/^\d{7,}$/.test(name) && !isOpaqueWireValue(name) ? name : '姓名待同步' }
export function accountStudentNumber(account:Account) { const value=(account.student_number || '').trim(); return value && !isOpaqueWireValue(value) ? value : '学号待同步' }
export function accountSelectorLabel(account:Account) { return `${accountDisplayName(account)} · ${account.login_name || accountStudentNumber(account)}` }
