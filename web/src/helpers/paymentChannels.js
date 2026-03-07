/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

const PAYMENT_SCOPE_ALL = 'all';
const PAYMENT_SCOPE_TOPUP = 'topup';
const PAYMENT_SCOPE_SUBSCRIPTION = 'subscription';

export const PAYMENT_CHANNEL_DEFAULTS = {
  alipay: {
    name: '支付宝',
    color: 'rgba(var(--semi-blue-5), 1)',
    enabled: true,
    scope: PAYMENT_SCOPE_ALL,
  },
  wxpay: {
    name: '微信',
    color: 'rgba(var(--semi-green-5), 1)',
    enabled: true,
    scope: PAYMENT_SCOPE_ALL,
  },
  stripe: {
    name: 'Stripe',
    color: 'rgba(var(--semi-purple-5), 1)',
    enabled: true,
    scope: PAYMENT_SCOPE_ALL,
  },
  creem: {
    name: 'Creem',
    color: 'rgba(var(--semi-orange-5), 1)',
    enabled: true,
    scope: PAYMENT_SCOPE_ALL,
  },
  bepusdt: {
    name: 'USDT支付',
    color: 'rgba(38, 161, 123, 1)',
    enabled: true,
    scope: PAYMENT_SCOPE_TOPUP,
  },
  futoon_alipay: {
    name: '富通支付宝',
    color: 'rgba(var(--semi-blue-5), 1)',
    enabled: false,
    scope: PAYMENT_SCOPE_ALL,
  },
  futoon_wxpay: {
    name: '富通微信',
    color: 'rgba(var(--semi-green-5), 1)',
    enabled: false,
    scope: PAYMENT_SCOPE_ALL,
  },
  custom1: {
    name: '自定义1',
    color: 'black',
    enabled: false,
    min_topup: '50',
    scope: PAYMENT_SCOPE_ALL,
  },
};

export const PAYMENT_CHANNEL_ORDER = [
  'alipay',
  'wxpay',
  'stripe',
  'creem',
  'bepusdt',
  'futoon_alipay',
  'futoon_wxpay',
  'custom1',
];

export const PAYMENT_SCOPE_OPTIONS = [
  { value: PAYMENT_SCOPE_ALL, label: '全部' },
  { value: PAYMENT_SCOPE_TOPUP, label: '仅充值' },
  { value: PAYMENT_SCOPE_SUBSCRIPTION, label: '仅订阅' },
];

export function getPaymentScopeLabel(scope) {
  switch (scope) {
    case PAYMENT_SCOPE_TOPUP:
      return '仅充值';
    case PAYMENT_SCOPE_SUBSCRIPTION:
      return '仅订阅';
    case PAYMENT_SCOPE_ALL:
    default:
      return '全部';
  }
}

export function normalizePaymentScope(scope) {
  if (
    scope === PAYMENT_SCOPE_ALL ||
    scope === PAYMENT_SCOPE_TOPUP ||
    scope === PAYMENT_SCOPE_SUBSCRIPTION
  ) {
    return scope;
  }
  return PAYMENT_SCOPE_ALL;
}

export function isPaymentMethodInScope(method, scene) {
  const scope = normalizePaymentScope(method?.scope);
  if (!scene || scope === PAYMENT_SCOPE_ALL) {
    return true;
  }
  return scope === scene;
}

export function isEpayLikeMethod(type) {
  return !['stripe', 'creem', 'bepusdt'].includes(type);
}

export function isFutoonMethod(type) {
  return ['futoon_alipay', 'futoon_wxpay'].includes(type);
}

function normalizeBoolean(value, fallback = true) {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'string') {
    if (value.toLowerCase() === 'true') return true;
    if (value.toLowerCase() === 'false') return false;
  }
  return fallback;
}

export function normalizePaymentChannel(method = {}, extraDefaults = {}) {
  const fallback = PAYMENT_CHANNEL_DEFAULTS[method?.type] || {};
  const merged = {
    ...fallback,
    ...extraDefaults,
    ...method,
  };

  const normalizedMinTopup = Number(merged.min_topup);
  return {
    type: merged.type || '',
    name: merged.name || merged.type || '',
    color:
      merged.color || fallback.color || 'rgba(var(--semi-primary-5), 1)',
    enabled: normalizeBoolean(merged.enabled, fallback.enabled ?? true),
    scope: normalizePaymentScope(merged.scope || fallback.scope),
    min_topup: Number.isFinite(normalizedMinTopup) ? normalizedMinTopup : 0,
  };
}

export function parsePaymentChannels(rawPayMethods) {
  let payMethods = rawPayMethods || [];
  if (typeof payMethods === 'string') {
    payMethods = JSON.parse(payMethods || '[]');
  }
  if (!Array.isArray(payMethods)) {
    return [];
  }

  const methods = payMethods
    .map((method) => normalizePaymentChannel(method))
    .filter((method) => method.type && method.name);

  return methods.sort((a, b) => {
    const aIndex = PAYMENT_CHANNEL_ORDER.indexOf(a.type);
    const bIndex = PAYMENT_CHANNEL_ORDER.indexOf(b.type);
    const normalizedA = aIndex === -1 ? Number.MAX_SAFE_INTEGER : aIndex;
    const normalizedB = bIndex === -1 ? Number.MAX_SAFE_INTEGER : bIndex;
    if (normalizedA !== normalizedB) {
      return normalizedA - normalizedB;
    }
    return a.type.localeCompare(b.type);
  });
}

export function buildPaymentChannelDrafts(rawPayMethods) {
  const parsed = parsePaymentChannels(rawPayMethods);
  const methodMap = new Map(parsed.map((method) => [method.type, method]));

  const drafts = [];
  const seen = new Set();
  PAYMENT_CHANNEL_ORDER.forEach((type) => {
    const method = methodMap.get(type);
    drafts.push(
      normalizePaymentChannel({ type, ...(method || PAYMENT_CHANNEL_DEFAULTS[type]) }),
    );
    seen.add(type);
  });

  parsed.forEach((method) => {
    if (!seen.has(method.type)) {
      drafts.push(normalizePaymentChannel(method));
    }
  });

  return drafts;
}

export function serializePaymentChannels(methods = []) {
  return JSON.stringify(
    methods.map((method) => ({
      type: method.type,
      name: method.name,
      enabled: !!method.enabled,
      color: method.color || '',
      min_topup:
        method.min_topup !== undefined && method.min_topup !== null
          ? String(method.min_topup)
          : '',
      scope: normalizePaymentScope(method.scope),
    })),
    null,
    2,
  );
}

export function getPaymentMethodName(type, payMethods = []) {
  const method = (payMethods || []).find((item) => item.type === type);
  if (method?.name) {
    return method.name;
  }
  return PAYMENT_CHANNEL_DEFAULTS[type]?.name || type || '-';
}
