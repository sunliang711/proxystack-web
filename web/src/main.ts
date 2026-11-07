import { createApp, type ComponentInternalInstance } from 'vue'
import App from './App.vue'
import router from './router'

// 生产构建的报错默认只有十层调用栈，常常停在框架内部，看不到是哪段页面代码触发的。
// stackTraceLimit 是 V8 的扩展（Chrome、Edge），其他浏览器忽略。
;(Error as { stackTraceLimit?: number }).stackTraceLimit = 50

const app = createApp(App)

/** 出错组件及其上层组件的名称，例如 Tabs < SettingsView < MainLayout。 */
function componentChain(instance: ComponentInternalInstance | null) {
  const names: string[] = []
  for (let current = instance; current && names.length < 12; current = current.parent) {
    const type = current.type as { __name?: string; name?: string }
    names.push(type.__name ?? type.name ?? '匿名组件')
  }
  return names.join(' < ') || '组件外'
}

// 生产构建的默认处理只打印错误本身；附上出错组件和阶段，界面异常时可以从控制台直接定位。
app.config.errorHandler = (error, instance, info) => {
  console.error(`[界面错误] ${componentChain(instance?.$ ?? null)}（${info}）`, error)
}

app.use(router).mount('#app')
