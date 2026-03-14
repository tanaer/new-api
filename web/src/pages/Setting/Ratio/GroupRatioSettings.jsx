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

import React, { useEffect, useState, useRef } from 'react';
import {
  Button,
  Col,
  Form,
  InputNumber,
  Row,
  Select,
  Space,
  Spin,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
  verifyJSON,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

const GROUP_ROUTE_POLICY_FIELD = 'group_ratio_setting.group_route_policy';

const defaultInputs = {
  GroupRatio: '',
  UserUsableGroups: '',
  GroupGroupRatio: '',
  'group_ratio_setting.group_special_usable_group': '',
  [GROUP_ROUTE_POLICY_FIELD]: '',
  AutoGroups: '',
  DefaultUseAutoGroup: false,
};

let groupRoutePolicyRowId = 0;

const createGroupRoutePolicyRow = ({
  userGroup = '',
  mode = 'profit_first',
  minSuccessRate = 85,
} = {}) => ({
  id: `group-route-policy-${groupRoutePolicyRowId++}`,
  userGroup,
  mode,
  minSuccessRate,
});

const isValidRoutePolicyMode = (mode) =>
  mode === 'profit_first' || mode === 'experience_first';

const createDefaultGroupRoutePolicyDraftRows = () => [
  createGroupRoutePolicyRow({
    userGroup: 'default',
    mode: 'profit_first',
    minSuccessRate: 85,
  }),
];

const parseGroupRoutePolicyToRows = (jsonStr) => {
  if (!jsonStr || jsonStr.trim() === '') {
    return { rows: [], error: '' };
  }

  try {
    const parsed = JSON.parse(jsonStr);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return {
        rows: [],
        error: '当前配置不是合法的对象结构，请编辑后重新保存',
      };
    }

    const rows = Object.entries(parsed).map(([userGroup, policy]) =>
      createGroupRoutePolicyRow({
        userGroup,
        mode:
          policy && isValidRoutePolicyMode(policy.mode)
            ? policy.mode
            : 'profit_first',
        minSuccessRate:
          policy && Number.isFinite(Number(policy.min_success_rate))
            ? Number(policy.min_success_rate)
            : 85,
      }),
    );

    return { rows, error: '' };
  } catch (error) {
    return {
      rows: [],
      error: '当前配置不是合法的 JSON，请编辑后重新保存',
    };
  }
};

const serializeGroupRoutePolicyRows = (rows) => {
  const routePolicy = {};

  rows.forEach((row) => {
    const userGroup = row.userGroup.trim();
    const minSuccessRate = Number(row.minSuccessRate);
    if (
      !userGroup ||
      !isValidRoutePolicyMode(row.mode) ||
      !Number.isFinite(minSuccessRate)
    ) {
      return;
    }

    routePolicy[userGroup] = {
      mode: row.mode,
      min_success_rate: minSuccessRate,
    };
  });

  return JSON.stringify(routePolicy, null, 2);
};

const validateGroupRoutePolicyRows = (rows) => {
  const groupSet = new Set();

  for (const row of rows) {
    const userGroup = row.userGroup.trim();
    if (!userGroup) {
      return '用户分组不能为空';
    }
    if (groupSet.has(userGroup)) {
      return '用户分组不能重复';
    }
    groupSet.add(userGroup);

    if (!isValidRoutePolicyMode(row.mode)) {
      return '请选择有效的路由模式';
    }

    const minSuccessRate = Number(row.minSuccessRate);
    if (!Number.isFinite(minSuccessRate)) {
      return '最低成功率必须为数字';
    }
    if (minSuccessRate < 0 || minSuccessRate > 100) {
      return '最低成功率必须在 0 到 100 之间';
    }
  }

  return '';
};

export default function GroupRatioSettings(props) {
  const { t } = useTranslation();
  const { Text } = Typography;
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);
  const [groupRoutePolicyRows, setGroupRoutePolicyRows] = useState([]);
  const [groupRoutePolicyLoadError, setGroupRoutePolicyLoadError] =
    useState('');
  const [groupOptions, setGroupOptions] = useState([]);

  const routeModeOptions = [
    { label: t('赚钱优先'), value: 'profit_first' },
    { label: t('体验优先'), value: 'experience_first' },
  ];
  const groupRoutePolicyError =
    validateGroupRoutePolicyRows(groupRoutePolicyRows);

  const updateGroupRoutePolicyRows = (updater) => {
    setGroupRoutePolicyRows((currentRows) => {
      const nextRows =
        typeof updater === 'function' ? updater(currentRows) : updater;
      setInputs((currentInputs) => ({
        ...currentInputs,
        [GROUP_ROUTE_POLICY_FIELD]: serializeGroupRoutePolicyRows(nextRows),
      }));
      return nextRows;
    });
    setGroupRoutePolicyLoadError('');
  };

  const handleAddGroupRoutePolicyRow = () => {
    updateGroupRoutePolicyRows((currentRows) => [
      ...currentRows,
      createGroupRoutePolicyRow({
        userGroup: currentRows.length === 0 ? 'default' : '',
      }),
    ]);
  };

  const fetchGroups = async () => {
    try {
      const res = await API.get('/api/group/');
      setGroupOptions(
        res.data.data.map((group) => ({ label: group, value: group })),
      );
    } catch (error) {
      showError(error.message);
    }
  };

  async function onSubmit() {
    try {
      if (
        inputs[GROUP_ROUTE_POLICY_FIELD] !== inputsRow[GROUP_ROUTE_POLICY_FIELD]
      ) {
        const routePolicyError =
          validateGroupRoutePolicyRows(groupRoutePolicyRows);
        if (routePolicyError) {
          return showError(t(routePolicyError));
        }
      }

      await refForm.current
        .validate()
        .then(() => {
          const updateArray = compareObjects(inputs, inputsRow);
          if (!updateArray.length)
            return showWarning(t('你似乎并没有修改什么'));

          const requestQueue = updateArray.map((item) => {
            const value =
              typeof inputs[item.key] === 'boolean'
                ? String(inputs[item.key])
                : inputs[item.key];
            return API.put('/api/option/', { key: item.key, value });
          });

          setLoading(true);
          Promise.all(requestQueue)
            .then((res) => {
              if (res.includes(undefined)) {
                return showError(
                  requestQueue.length > 1
                    ? t('部分保存失败，请重试')
                    : t('保存失败'),
                );
              }

              for (let i = 0; i < res.length; i++) {
                if (!res[i].data.success) {
                  return showError(res[i].data.message);
                }
              }

              showSuccess(t('保存成功'));
              props.refresh();
            })
            .catch((error) => {
              console.error('Unexpected error:', error);
              showError(t('保存失败，请重试'));
            })
            .finally(() => {
              setLoading(false);
            });
        })
        .catch(() => {
          showError(t('请检查输入'));
        });
    } catch (error) {
      showError(t('请检查输入'));
      console.error(error);
    }
  }

  useEffect(() => {
    const currentInputs = { ...defaultInputs };
    for (let key in props.options) {
      if (Object.prototype.hasOwnProperty.call(defaultInputs, key)) {
        currentInputs[key] = props.options[key];
      }
    }
    const { rows, error } = parseGroupRoutePolicyToRows(
      currentInputs[GROUP_ROUTE_POLICY_FIELD],
    );
    setGroupRoutePolicyRows(
      error || rows.length > 0
        ? rows
        : createDefaultGroupRoutePolicyDraftRows(),
    );
    setGroupRoutePolicyLoadError(error);
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current?.setValues(currentInputs);
  }, [props.options]);

  useEffect(() => {
    fetchGroups();
  }, []);

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        getFormApi={(formAPI) => (refForm.current = formAPI)}
        style={{ marginBottom: 15 }}
      >
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.TextArea
              label={t('分组倍率')}
              placeholder={t('为一个 JSON 文本，键为分组名称，值为倍率')}
              extraText={t(
                '分组倍率设置，可以在此处新增分组或修改现有分组的倍率，格式为 JSON 字符串，例如：{"vip": 0.5, "test": 1}，表示 vip 分组的倍率为 0.5，test 分组的倍率为 1',
              )}
              field={'GroupRatio'}
              autosize={{ minRows: 6, maxRows: 12 }}
              trigger='blur'
              stopValidateWithError
              rules={[
                {
                  validator: (rule, value) => verifyJSON(value),
                  message: t('不是合法的 JSON 字符串'),
                },
              ]}
              onChange={(value) => setInputs({ ...inputs, GroupRatio: value })}
            />
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.TextArea
              label={t('用户可选分组')}
              placeholder={t('为一个 JSON 文本，键为分组名称，值为分组描述')}
              extraText={t(
                '用户新建令牌时可选的分组，格式为 JSON 字符串，例如：{"vip": "VIP 用户", "test": "测试"}，表示用户可以选择 vip 分组和 test 分组',
              )}
              field={'UserUsableGroups'}
              autosize={{ minRows: 6, maxRows: 12 }}
              trigger='blur'
              stopValidateWithError
              rules={[
                {
                  validator: (rule, value) => verifyJSON(value),
                  message: t('不是合法的 JSON 字符串'),
                },
              ]}
              onChange={(value) =>
                setInputs({ ...inputs, UserUsableGroups: value })
              }
            />
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.TextArea
              label={t('分组特殊倍率')}
              placeholder={t('为一个 JSON 文本')}
              extraText={t(
                '键为分组名称，值为另一个 JSON 对象，键为分组名称，值为该分组的用户的特殊分组倍率，例如：{"vip": {"default": 0.5, "test": 1}}，表示 vip 分组的用户在使用default分组的令牌时倍率为0.5，使用test分组时倍率为1',
              )}
              field={'GroupGroupRatio'}
              autosize={{ minRows: 6, maxRows: 12 }}
              trigger='blur'
              stopValidateWithError
              rules={[
                {
                  validator: (rule, value) => verifyJSON(value),
                  message: t('不是合法的 JSON 字符串'),
                },
              ]}
              onChange={(value) =>
                setInputs({ ...inputs, GroupGroupRatio: value })
              }
            />
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.TextArea
              label={t('分组特殊可用分组')}
              placeholder={t('为一个 JSON 文本')}
              extraText={t(
                '键为用户分组名称，值为操作映射对象。内层键以"+:"开头表示添加指定分组（键值为分组名称，值为描述），以"-:"开头表示移除指定分组（键值为分组名称），不带前缀的键直接添加该分组。例如：{"vip": {"+:premium": "高级分组", "special": "特殊分组", "-:default": "默认分组"}}，表示 vip 分组的用户可以使用 premium 和 special 分组，同时移除 default 分组的访问权限',
              )}
              field={'group_ratio_setting.group_special_usable_group'}
              autosize={{ minRows: 6, maxRows: 12 }}
              trigger='blur'
              stopValidateWithError
              rules={[
                {
                  validator: (rule, value) => verifyJSON(value),
                  message: t('不是合法的 JSON 字符串'),
                },
              ]}
              onChange={(value) =>
                setInputs({
                  ...inputs,
                  'group_ratio_setting.group_special_usable_group': value,
                })
              }
            />
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.Slot label={t('用户分组路由策略')}>
              <div
                style={{
                  width: '100%',
                  padding: 16,
                  border: '1px solid var(--semi-color-border)',
                  borderRadius: 12,
                  background: 'var(--semi-color-fill-0)',
                }}
              >
                <Space
                  vertical
                  align='start'
                  spacing='medium'
                  style={{ width: '100%' }}
                >
                  <div>
                    <Text type='tertiary'>
                      {t(
                        '按用户分组配置路由模式。赚钱优先会优先成本更低的上游通道，只要求满足最低成功率；体验优先会优先成功率更高的通道，再兼顾成本。',
                      )}
                    </Text>
                    <br />
                    <Text type='tertiary'>
                      {t(
                        '最低成功率范围为 0-100。若某个用户分组没有单独配置，将回退使用 default 的策略。',
                      )}
                    </Text>
                  </div>

                  {groupRoutePolicyLoadError ? (
                    <Text type='danger'>{t(groupRoutePolicyLoadError)}</Text>
                  ) : (
                    groupRoutePolicyError && (
                      <Text type='danger'>{t(groupRoutePolicyError)}</Text>
                    )
                  )}

                  {groupRoutePolicyRows.length === 0 ? (
                    <div
                      style={{
                        width: '100%',
                        padding: '16px 12px',
                        borderRadius: 10,
                        border: '1px dashed var(--semi-color-border)',
                      }}
                    >
                      <Text type='tertiary'>
                        {t('当前还没有配置用户分组路由策略')}
                      </Text>
                    </div>
                  ) : (
                    groupRoutePolicyRows.map((row, index) => (
                      <div
                        key={row.id}
                        style={{
                          width: '100%',
                          padding: 16,
                          border: '1px solid var(--semi-color-border)',
                          borderRadius: 10,
                          background: 'var(--semi-color-bg-1)',
                        }}
                      >
                        <Row gutter={12}>
                          <Col xs={24} md={8}>
                            <div
                              style={{
                                marginBottom: 8,
                                fontSize: 12,
                                color: 'var(--semi-color-text-2)',
                              }}
                            >
                              {t('用户分组')}
                            </div>
                            <Select
                              value={row.userGroup}
                              placeholder={t('例如 default、vip')}
                              optionList={groupOptions}
                              allowAdditions
                              search
                              style={{ width: '100%' }}
                              onChange={(value) =>
                                updateGroupRoutePolicyRows((currentRows) =>
                                  currentRows.map((item) =>
                                    item.id === row.id
                                      ? { ...item, userGroup: value }
                                      : item,
                                  ),
                                )
                              }
                            />
                          </Col>
                          <Col xs={24} md={8}>
                            <div
                              style={{
                                marginBottom: 8,
                                fontSize: 12,
                                color: 'var(--semi-color-text-2)',
                              }}
                            >
                              {t('路由模式')}
                            </div>
                            <Select
                              value={row.mode}
                              optionList={routeModeOptions}
                              placeholder={t('请选择路由模式')}
                              style={{ width: '100%' }}
                              onChange={(value) =>
                                updateGroupRoutePolicyRows((currentRows) =>
                                  currentRows.map((item) =>
                                    item.id === row.id
                                      ? { ...item, mode: value }
                                      : item,
                                  ),
                                )
                              }
                            />
                          </Col>
                          <Col xs={18} md={6}>
                            <div
                              style={{
                                marginBottom: 8,
                                fontSize: 12,
                                color: 'var(--semi-color-text-2)',
                              }}
                            >
                              {t('最低成功率')}
                            </div>
                            <InputNumber
                              value={row.minSuccessRate}
                              min={0}
                              max={100}
                              precision={2}
                              placeholder={85}
                              style={{ width: '100%' }}
                              onChange={(value) =>
                                updateGroupRoutePolicyRows((currentRows) =>
                                  currentRows.map((item) =>
                                    item.id === row.id
                                      ? { ...item, minSuccessRate: value }
                                      : item,
                                  ),
                                )
                              }
                            />
                          </Col>
                          <Col xs={6} md={2}>
                            <div
                              style={{
                                marginBottom: 8,
                                fontSize: 12,
                                color: 'var(--semi-color-text-2)',
                              }}
                            >
                              {t('操作')}
                            </div>
                            <Button
                              type='danger'
                              theme='borderless'
                              icon={<IconDelete />}
                              aria-label={t('删除策略')}
                              onClick={() =>
                                updateGroupRoutePolicyRows((currentRows) =>
                                  currentRows.filter(
                                    (item) => item.id !== row.id,
                                  ),
                                )
                              }
                            />
                          </Col>
                        </Row>
                        <div
                          style={{
                            marginTop: 12,
                            fontSize: 12,
                            color: 'var(--semi-color-text-2)',
                          }}
                        >
                          {t('策略')} #{index + 1}
                        </div>
                      </div>
                    ))
                  )}

                  <Button
                    icon={<IconPlus />}
                    onClick={handleAddGroupRoutePolicyRow}
                  >
                    {t('添加策略')}
                  </Button>
                </Space>
              </div>
            </Form.Slot>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} sm={16}>
            <Form.TextArea
              label={t('自动分组auto，从第一个开始选择')}
              placeholder={t('为一个 JSON 文本')}
              field={'AutoGroups'}
              autosize={{ minRows: 6, maxRows: 12 }}
              trigger='blur'
              stopValidateWithError
              rules={[
                {
                  validator: (rule, value) => {
                    if (!value || value.trim() === '') {
                      return true; // Allow empty values
                    }

                    // First check if it's valid JSON
                    try {
                      const parsed = JSON.parse(value);

                      // Check if it's an array
                      if (!Array.isArray(parsed)) {
                        return false;
                      }

                      // Check if every element is a string
                      return parsed.every((item) => typeof item === 'string');
                    } catch (error) {
                      return false;
                    }
                  },
                  message: t('必须是有效的 JSON 字符串数组，例如：["g1","g2"]'),
                },
              ]}
              onChange={(value) => setInputs({ ...inputs, AutoGroups: value })}
            />
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={16}>
            <Form.Switch
              label={t(
                '创建令牌默认选择auto分组，初始令牌也将设为auto（否则留空，为用户默认分组）',
              )}
              field={'DefaultUseAutoGroup'}
              onChange={(value) =>
                setInputs({ ...inputs, DefaultUseAutoGroup: value })
              }
            />
          </Col>
        </Row>
      </Form>
      <Button onClick={onSubmit}>{t('保存分组倍率设置')}</Button>
    </Spin>
  );
}
