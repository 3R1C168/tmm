import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/messages' },
  { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { title: '登录' } },
  { path: '/messages', name: 'messages', component: () => import('./views/Messages.vue'), meta: { title: '消息' } },
  { path: '/tasks', name: 'tasks', component: () => import('./views/Tasks.vue'), meta: { title: '任务' } },
  { path: '/settings', name: 'settings', component: () => import('./views/Settings.vue'), meta: { title: '账号' } },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})
