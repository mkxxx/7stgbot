// sw.js
const CACHE_NAME = 'gate-v1';

// Простейшая установка сервис-воркера
self.addEventListener('install', (event) => {
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    event.waitUntil(self.clients.claim());
});

// Перехват запросов (необходим для работы PWA)
self.addEventListener('fetch', (event) => {
    event.respondWith(fetch(event.request));
});
