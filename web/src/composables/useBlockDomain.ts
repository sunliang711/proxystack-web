import { ref } from 'vue'
import { useMessage } from 'naive-ui'
import { api, problemMessage, type Schemas } from '../api/client'
import { refreshPending } from '../runtime'

export type BlockListed = Schemas['AccessDomainCount']['listed']

/** 把主域名加入或移出全局（userId 为 0）或某个用户的域名黑名单；改动进入待应用变更，应用后生效。 */
export function useBlockDomain() {
  const message = useMessage()
  const busy = ref('')

  async function setBlocked(domain: string, userId: number, blocked: boolean): Promise<BlockListed | null> {
    busy.value = domain
    const { data, error } = await api.POST('/api/access/block', { body: { domain, user_id: userId, blocked } })
    busy.value = ''
    if (!data) {
      message.error(problemMessage(error, blocked ? '拉黑失败' : '移出失败'))
      return null
    }
    const scope = userId ? '该用户的域名黑名单' : '全局域名黑名单'
    if (!data.changed) message.info(`${data.domain} ${blocked ? '已在' : '不在'}${scope}中`)
    else message.success(`已把 ${data.domain} ${blocked ? '加入' : '移出'}${scope}，应用配置后生效`)
    void refreshPending()
    return data.listed
  }

  return { busy, setBlocked }
}
