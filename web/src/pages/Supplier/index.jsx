import React, { useState, useEffect, useCallback } from 'react';
import {
    Table,
    Button,
    Modal,
    Form,
    Space,
    Tag,
    Popconfirm,
    Toast,
    InputNumber,
    Typography,
    SideSheet,
    Descriptions,
    Select,
    Input,
    Banner,
    Spin,
    Collapsible,
} from '@douyinfe/semi-ui';
import {
    IconPlus,
    IconRefresh,
    IconDelete,
    IconEdit,
    IconSetting,
    IconSearch,
    IconSync,
} from '@douyinfe/semi-icons';
import { API } from '../../helpers/api';

const { Text } = Typography;

const API_BASE = '/api/supplier';

const SupplierPage = () => {
    const [suppliers, setSuppliers] = useState([]);
    const [loading, setLoading] = useState(false);
    const [showEdit, setShowEdit] = useState(false);
    const [editingSupplier, setEditingSupplier] = useState(null);
    const [showGroups, setShowGroups] = useState(false);
    const [currentSupplier, setCurrentSupplier] = useState(null);
    const [groups, setGroups] = useState([]);
    const [groupsLoading, setGroupsLoading] = useState(false);
    const [fetchingGroups, setFetchingGroups] = useState(false);
    const [fetchingGroupsWithKeys, setFetchingGroupsWithKeys] = useState(false);
    const [bulkMarkupVisible, setBulkMarkupVisible] = useState(false);
    const [bulkMarkup, setBulkMarkup] = useState(1.1);
    const [existingGroups, setExistingGroups] = useState([]);
    const [syncingFull, setSyncingFull] = useState(false);
    const [syncingAllFull, setSyncingAllFull] = useState(false);

    const loadSuppliers = useCallback(async () => {
        setLoading(true);
        try {
            const res = await API.get(`${API_BASE}/`);
            if (res.data.success) setSuppliers(res.data.data || []);
        } finally {
            setLoading(false);
        }
    }, []);

    // 加载本地分组列表
    const loadExistingGroups = useCallback(async () => {
        try {
            const res = await API.get('/api/group/');
            if (res.data.success) {
                const groupNames = res.data.data || [];
                setExistingGroups(
                    groupNames.map((g) => ({ label: g, value: g })),
                );
            }
        } catch (e) {
            // ignore
        }
    }, []);

    useEffect(() => {
        loadSuppliers();
        loadExistingGroups();
    }, [loadSuppliers, loadExistingGroups]);

    // 打开编辑
    const openEdit = (supplier = null) => {
        setEditingSupplier(
            supplier || { name: '', base_url: '', username: '', password: '', cookie: '', upstream_user_id: 0, markup: 1.1, status: 1 },
        );
        setShowEdit(true);
    };

    // 保存供应商
    const saveSupplier = async (values) => {
        const isNew = !editingSupplier?.id;
        const method = isNew ? 'post' : 'put';
        const body = { ...editingSupplier, ...values };
        try {
            const res = await API[method](`${API_BASE}/`, body);
            if (res.data.success) {
                Toast.success(res.data.message);
                setShowEdit(false);
                loadSuppliers();
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('请求失败');
        }
    };

    // 删除供应商
    const deleteSupplier = async (id) => {
        try {
            const res = await API.delete(`${API_BASE}/${id}`);
            if (res.data.success) {
                Toast.success(res.data.message);
                loadSuppliers();
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('删除失败');
        }
    };

    // 打开分组管理
    const openGroups = async (supplier) => {
        setCurrentSupplier(supplier);
        setShowGroups(true);
        setGroupsLoading(true);
        try {
            const res = await API.get(`${API_BASE}/${supplier.id}`);
            if (res.data.success) {
                setGroups(res.data.data.groups || []);
            }
        } finally {
            setGroupsLoading(false);
        }
    };

    // 一键采集分组
    const fetchGroups = async () => {
        if (!currentSupplier) return;
        setFetchingGroups(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/fetch_groups`);
            if (res.data.success) {
                Toast.success(res.data.message);
                setGroups(res.data.data || []);
            } else {
                Toast.error(res.data.message);
            }
        } finally {
            setFetchingGroups(false);
        }
    };

    // 采集分组并自动创建/回填分组密钥
    const fetchGroupsWithKeys = async () => {
        if (!currentSupplier) return;
        setFetchingGroupsWithKeys(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/fetch_groups_with_keys`);
            if (res.data.data) {
                setGroups(res.data.data || []);
            }
            if (res.data.success) {
                Toast.success(res.data.message);
            } else if (res.data.data) {
                Toast.warning(res.data.message);
            } else {
                Toast.error(res.data.message);
            }

            if (Array.isArray(res.data.warnings) && res.data.warnings.length > 0) {
                Modal.warning({
                    title: '部分分组密钥处理失败',
                    content: (
                        <div style={{ maxHeight: 280, overflow: 'auto' }}>
                            {res.data.warnings.map((msg, idx) => (
                                <div key={idx} style={{ marginBottom: 6 }}>
                                    {msg}
                                </div>
                            ))}
                        </div>
                    ),
                });
            }
            loadSuppliers();
        } finally {
            setFetchingGroupsWithKeys(false);
        }
    };

    // 更新分组配置
    const updateGroup = async (group) => {
        try {
            const res = await API.put(`${API_BASE}/group`, group);
            if (res.data.success) {
                Toast.success('更新成功');
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('更新失败');
        }
    };

    // 修改单个供应商倍率
    const updateMarkup = async (supplierId, markup) => {
        try {
            const res = await API.put(`${API_BASE}/${supplierId}/markup`, { markup });
            if (res.data.success) {
                Toast.success(res.data.message);
                loadSuppliers();
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('更新失败');
        }
    };

    // 一键设置所有倍率
    const bulkUpdateMarkup = async () => {
        try {
            const res = await API.put(`${API_BASE}/bulk_markup`, { markup: bulkMarkup });
            if (res.data.success) {
                Toast.success(res.data.message);
                setBulkMarkupVisible(false);
                loadSuppliers();
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('更新失败');
        }
    };

    // 查询余额
    const checkBalance = async (supplierId) => {
        try {
            const res = await API.post(`${API_BASE}/${supplierId}/check_balance`);
            if (res.data.success) {
                const data = res.data.data || {};
                const remaining = Number(data.remaining_quota || 0);
                const used = data.used_quota !== null && data.used_quota !== undefined
                    ? Number(data.used_quota)
                    : null;
                const total = data.total_quota !== null && data.total_quota !== undefined
                    ? Number(data.total_quota)
                    : null;
                const username = data.display_name || data.username || '';
                const userLabel = username ? `（${username}）` : '';

                let msg = `余额信息${userLabel}: 剩余 ${remaining.toLocaleString()}`;
                if (used !== null) {
                    msg += `，已用 ${used.toLocaleString()}`;
                }
                if (total !== null) {
                    msg += `，总额 ${total.toLocaleString()}`;
                }
                Toast.info(msg);
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('查询失败');
        }
    };

    // 一键同步（一站式）
    const syncFull = async () => {
        if (!currentSupplier) return;
        setSyncingFull(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/sync_full`);
            if (res.data.success) {
                Toast.success(res.data.message);
                if (res.data.groups) {
                    setGroups(res.data.groups);
                }
            } else {
                Toast.error(res.data.message);
            }
            if (Array.isArray(res.data.warnings) && res.data.warnings.length > 0) {
                Modal.warning({
                    title: '同步警告',
                    content: (
                        <div style={{ maxHeight: 280, overflow: 'auto' }}>
                            {res.data.warnings.map((msg, idx) => (
                                <div key={idx} style={{ marginBottom: 6 }}>
                                    {msg}
                                </div>
                            ))}
                        </div>
                    ),
                });
            }
            loadSuppliers();
        } finally {
            setSyncingFull(false);
        }
    };

    // 同步所有供应商
    const syncAllFull = async () => {
        setSyncingAllFull(true);
        try {
            const res = await API.post(`${API_BASE}/sync_all_full`);
            if (res.data.success) {
                Toast.success(res.data.message);
            } else {
                Toast.error(res.data.message);
            }
            if (Array.isArray(res.data.warnings) && res.data.warnings.length > 0) {
                Modal.warning({
                    title: '同步警告',
                    content: (
                        <div style={{ maxHeight: 280, overflow: 'auto' }}>
                            {res.data.warnings.map((msg, idx) => (
                                <div key={idx} style={{ marginBottom: 6 }}>
                                    {msg}
                                </div>
                            ))}
                        </div>
                    ),
                });
            }
            loadSuppliers();
        } finally {
            setSyncingAllFull(false);
        }
    };

    const columns = [
        { title: 'ID', dataIndex: 'id', width: 60 },
        { title: '名称', dataIndex: 'name', width: 150 },
        { title: '上游用户ID', dataIndex: 'upstream_user_id', width: 120 },
        {
            title: 'API 地址',
            dataIndex: 'base_url',
            width: 250,
            render: (text) => (
                <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 230 }}>
                    {text}
                </Text>
            ),
        },
        {
            title: '倍率',
            dataIndex: 'markup',
            width: 100,
            render: (text, record) => (
                <Tag color={text >= 1 ? 'green' : 'red'} style={{ cursor: 'pointer' }}>
                    ×{text?.toFixed(2)}
                </Tag>
            ),
        },
        {
            title: '状态',
            dataIndex: 'status',
            width: 80,
            render: (status) => (
                <Tag color={status === 1 ? 'green' : 'red'}>
                    {status === 1 ? '启用' : '禁用'}
                </Tag>
            ),
        },
        {
            title: '操作',
            width: 300,
            render: (_, record) => (
                <Space>
                    <Button size='small' icon={<IconEdit />} onClick={() => openEdit(record)}>
                        编辑
                    </Button>
                    <Button size='small' icon={<IconSetting />} onClick={() => openGroups(record)}>
                        分组
                    </Button>
                    <Button size='small' onClick={() => checkBalance(record.id)}>
                        余额
                    </Button>
                    <Popconfirm title='确认删除？' onConfirm={() => deleteSupplier(record.id)}>
                        <Button size='small' type='danger' icon={<IconDelete />}>
                            删除
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    // 分组列
    const groupColumns = [
        { title: '上游分组', dataIndex: 'upstream_group', width: 120 },
        {
            title: '分组倍率',
            dataIndex: 'group_ratio',
            width: 100,
            render: (v) => <Tag color='blue'>×{v?.toFixed(4)}</Tag>,
        },
        {
            title: 'API密钥',
            dataIndex: 'api_key',
            width: 200,
            render: (text, record, index) => (
                <Input
                    defaultValue={text}
                    placeholder='输入API密钥'
                    size='small'
                    onBlur={(e) => {
                        const newGroups = [...groups];
                        newGroups[index] = { ...newGroups[index], api_key: e.target.value };
                        setGroups(newGroups);
                        updateGroup({ ...newGroups[index] });
                    }}
                />
            ),
        },
        {
            title: '本地分组',
            dataIndex: 'local_group',
            width: 180,
            render: (text, record, index) => (
                <Select
                    defaultValue={text || undefined}
                    placeholder='选择本地分组'
                    size='small'
                    filter
                    allowCreate
                    style={{ width: 160 }}
                    optionList={existingGroups}
                    onChange={(value) => {
                        const newGroups = [...groups];
                        newGroups[index] = { ...newGroups[index], local_group: value };
                        setGroups(newGroups);
                        updateGroup({ ...newGroups[index] });
                    }}
                />
            ),
        },
        {
            title: '通道类型',
            dataIndex: 'endpoint_type',
            width: 100,
            render: (v) => <Tag>{v || 'openai'}</Tag>,
        },
        {
            title: '支持模型',
            dataIndex: 'supported_models',
            width: 150,
            render: (v) => {
                if (!v) return <Text type='tertiary'>-</Text>;
                const models = v.split(',').filter(m => m);
                if (models.length === 0) return <Text type='tertiary'>-</Text>;
                if (models.length <= 3) return <Text>{models.join(', ')}</Text>;
                return <Text>{models.slice(0, 3).join(', ')}... (+{models.length - 3})</Text>;
            },
        },
    ];

    return (
        <div className='mt-[60px] px-2'>
            <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Space>
                    <Button icon={<IconPlus />} theme='solid' onClick={() => openEdit()}>
                        添加供应商
                    </Button>
                    <Button icon={<IconRefresh />} onClick={loadSuppliers}>
                        刷新
                    </Button>
                </Space>
                <Space>
                    <Button
                        theme='solid'
                        type='primary'
                        icon={<IconSync />}
                        loading={syncingAllFull}
                        onClick={syncAllFull}
                    >
                        一键同步所有供应商
                    </Button>
                    <Button theme='light' type='warning' onClick={() => setBulkMarkupVisible(true)}>
                        一键设置所有倍率
                    </Button>
                </Space>
            </div>

            <Table
                columns={columns}
                dataSource={suppliers}
                rowKey='id'
                loading={loading}
                pagination={false}
            />

            {/* 编辑供应商 SideSheet */}
            <SideSheet
                title={editingSupplier?.id ? '编辑供应商' : '添加供应商'}
                visible={showEdit}
                onCancel={() => setShowEdit(false)}
                width={480}
            >
                {editingSupplier && (
                    <Form
                        initValues={editingSupplier}
                        onSubmit={saveSupplier}
                        labelPosition='left'
                        labelWidth={100}
                    >
                        <Form.Input field='name' label='名称' rules={[{ required: true, message: '请输入名称' }]} />
                        <Form.Input field='base_url' label='API地址' rules={[{ required: true, message: '请输入API地址' }]} />
                        <Form.Input field='username' label='账号' />
                        <Form.Input field='password' label='密码' mode='password' />
                        <Form.TextArea field='cookie' label='Cookie/Token' placeholder='可填 Cookie 或 AccessToken（支持 sk-xxx / Bearer xxx）' autosize rows={2} />
                        <Form.InputNumber field='upstream_user_id' label='上游用户ID' min={0} step={1} />
                        <Form.InputNumber field='markup' label='加价倍率' min={0} step={0.01} />
                        <Form.Select field='status' label='状态' optionList={[
                            { label: '启用', value: 1 },
                            { label: '禁用', value: 2 },
                        ]} />
                        <div style={{ marginTop: 16 }}>
                            <Space>
                                <Button htmlType='submit' theme='solid'>保存</Button>
                                <Button onClick={() => setShowEdit(false)}>取消</Button>
                            </Space>
                        </div>
                    </Form>
                )}
            </SideSheet>

            {/* 分组管理 SideSheet */}
            <SideSheet
                title={`分组管理 - ${currentSupplier?.name || ''}`}
                visible={showGroups}
                onCancel={() => setShowGroups(false)}
                width={900}
            >
                <div style={{ marginBottom: 16 }}>
                    <Space wrap>
                        <Button
                            icon={<IconSync />}
                            loading={syncingFull}
                            onClick={syncFull}
                            theme='solid'
                            type='primary'
                            size='large'
                        >
                            一键同步（推荐）
                        </Button>
                        <Button icon={<IconSearch />} loading={fetchingGroups} onClick={fetchGroups}>
                            仅采集分组
                        </Button>
                        <Button
                            loading={fetchingGroupsWithKeys}
                            onClick={fetchGroupsWithKeys}
                            type='warning'
                        >
                            采集+生成密钥
                        </Button>
                    </Space>
                    {currentSupplier && (
                        <Descriptions
                            row
                            size='small'
                            style={{ marginTop: 12 }}
                            data={[
                                { key: '当前倍率', value: `×${currentSupplier.markup?.toFixed(2)}` },
                                { key: '上游用户ID', value: currentSupplier.upstream_user_id || 0 },
                            ]}
                        />
                    )}
                </div>

                {groupsLoading ? (
                    <Spin size='large' />
                ) : groups.length > 0 ? (
                    <Table
                        columns={groupColumns}
                        dataSource={groups}
                        rowKey='id'
                        pagination={false}
                        size='small'
                    />
                ) : (
                    <Banner type='info' description='暂无分组数据，请先采集分组或使用一键同步' />
                )}

                {/* 操作说明 */}
                <Collapsible style={{ marginTop: 24 }}>
                    <Collapsible.Header title='操作说明'>
                        <Text>点击展开/收起</Text>
                    </Collapsible.Header>
                    <Collapsible.Body>
                        <div style={{ padding: 12, background: 'var(--semi-color-fill-0)', borderRadius: 8 }}>
                            <Text>
                                <strong>一键同步会自动完成以下操作：</strong><br/>
                                1. 从上游 /api/pricing 采集分组信息（倍率、支持的模型、通道类型）<br/>
                                2. 自动映射本地分组（根据名称规则：cc→cc开头、codex/openai→codex开头、gemini→gemini开头，找倍率最接近的）<br/>
                                3. 为每个分组生成/回填 API 密钥<br/>
                                4. 同步倍率到系统（只同步已映射的本地分组，不会把上游分组加入系统）<br/>
                                5. 创建/更新渠道（自动填充分组、模型、通道类型）<br/>
                                6. 禁用上游已不存在的渠道<br/><br/>
                                <strong>建议流程：</strong> 点击「一键同步」→ 检查本地分组映射是否正确 → 如需调整手动修改 → 再次点击「一键同步」
                            </Text>
                        </div>
                    </Collapsible.Body>
                </Collapsible>

                {currentSupplier && (
                    <div style={{ marginTop: 16, padding: 16, background: 'var(--semi-color-fill-0)', borderRadius: 8 }}>
                        <Text strong>调整倍率并重算通道状态</Text>
                        <div style={{ marginTop: 8, display: 'flex', gap: 8, alignItems: 'center' }}>
                            <InputNumber
                                defaultValue={currentSupplier.markup}
                                min={0}
                                step={0.01}
                                style={{ width: 120 }}
                                onChange={(v) => setCurrentSupplier({ ...currentSupplier, markup: v })}
                            />
                            <Button
                                theme='solid'
                                type='warning'
                                onClick={() => updateMarkup(currentSupplier.id, currentSupplier.markup)}
                            >
                                应用并调整通道
                            </Button>
                        </div>
                    </div>
                )}
            </SideSheet>

            {/* 一键设置倍率 Modal */}
            <Modal
                title='一键设置所有供应商倍率'
                visible={bulkMarkupVisible}
                onCancel={() => setBulkMarkupVisible(false)}
                onOk={bulkUpdateMarkup}
                okText='确认设置'
            >
                <div style={{ padding: '16px 0' }}>
                    <Text>设置后，所有供应商的倍率将统一调整为指定值，并自动重算通道状态。</Text>
                    <div style={{ marginTop: 16 }}>
                        <InputNumber
                            value={bulkMarkup}
                            min={0}
                            step={0.01}
                            style={{ width: 200 }}
                            onChange={setBulkMarkup}
                            prefix='×'
                        />
                    </div>
                </div>
            </Modal>
        </div>
    );
};

export default SupplierPage;
