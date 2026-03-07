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
    Checkbox,
    Divider,
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
    
    // 新增状态：未映射分组和选中状态
    const [unmappedGroups, setUnmappedGroups] = useState([]);
    const [selectedGroupIds, setSelectedGroupIds] = useState([]);
    const [batchCreating, setBatchCreating] = useState(false);

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
                Toast.success('删除成功');
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
        setSelectedGroupIds([]);
        setUnmappedGroups([]);
        try {
            const res = await API.get(`${API_BASE}/${supplier.id}/groups`);
            if (res.data.success) {
                setGroups(res.data.data || []);
            }
        } finally {
            setGroupsLoading(false);
        }
    };

    // 采集分组
    const fetchGroups = async () => {
        if (!currentSupplier) return;
        setFetchingGroups(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/fetch_groups`);
            if (res.data.data) {
                setGroups(res.data.data || []);
            }
            if (res.data.success) {
                Toast.success(res.data.message);
            } else {
                Toast.error(res.data.message);
            }
            loadSuppliers();
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
            const res = await API.put(`${API_BASE}/group`, {
                id: group.id,
                local_group: group.local_group,
                group_ratio: group.group_ratio,
            });
            if (res.data.success) {
                Toast.success('更新成功');
                // 刷新分组列表
                const groupsRes = await API.get(`${API_BASE}/${currentSupplier.id}/groups`);
                if (groupsRes.data.success) {
                    setGroups(groupsRes.data.data || []);
                }
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('更新失败');
        }
    };

    // 更新供应商倍率
    const updateMarkup = async (id, markup) => {
        try {
            const res = await API.put(`${API_BASE}/${id}/markup`, { markup });
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

    // 检查余额
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

    // 一键更新渠道（一站式）
    const syncFull = async () => {
        if (!currentSupplier) return;
        setSyncingFull(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/sync_full`);
            const data = res.data || {};
            const partial = !!data.partial_success;
            if (data.success && partial) {
                Toast.warning(data.message || '更新完成，但有部分问题');
            } else if (data.success) {
                Toast.success(data.message);
            } else {
                Toast.error(res.data.message);
            }

            if (data.groups) {
                setGroups(data.groups);
            }

            // 处理未映射分组
            if (Array.isArray(data.unmapped_groups) && data.unmapped_groups.length > 0) {
                setUnmappedGroups(data.unmapped_groups);
            } else {
                setUnmappedGroups([]);
            }

            const hasWarnings = Array.isArray(data.warnings) && data.warnings.length > 0;
            const hasSteps = Array.isArray(data.steps) && data.steps.length > 0;
            const shouldShowDetail = hasWarnings || partial || !data.success;
            if (shouldShowDetail) {
                Modal.warning({
                    title: '更新结果详情',
                    content: (
                        <div style={{ maxHeight: 280, overflow: 'auto' }}>
                            {hasSteps && (
                                <div style={{ marginBottom: 10 }}>
                                    <Text strong>执行步骤</Text>
                                    {data.steps.map((step, idx) => (
                                        <div key={`step-${idx}`} style={{ marginTop: 4 }}>
                                            <Tag color={step.success ? 'green' : 'red'} style={{ marginRight: 8 }}>
                                                {step.name}
                                            </Tag>
                                            <Text>{step.success ? '成功' : '失败'} · {step.cost_ms || 0}ms</Text>
                                            {step.message && (
                                                <div style={{ marginTop: 2 }}>
                                                    <Text type='tertiary'>{step.message}</Text>
                                                </div>
                                            )}
                                        </div>
                                    ))}
                                </div>
                            )}
                            {hasWarnings && (
                                <>
                                    <Text strong>警告信息</Text>
                                    {data.warnings.map((msg, idx) => (
                                        <div key={`warn-${idx}`} style={{ marginTop: 4 }}>
                                            {msg}
                                        </div>
                                    ))}
                                </>
                            )}
                        </div>
                    ),
                });
            }
            loadSuppliers();
        } finally {
            setSyncingFull(false);
        }
    };

    // 更新所有供应商
    const syncAllFull = async () => {
        setSyncingAllFull(true);
        try {
            const res = await API.post(`${API_BASE}/sync_all_full`);
            const data = res.data || {};
            const partial = !!data.partial_success;
            if (data.success && partial) {
                Toast.warning(data.message || '批量更新完成，但有部分问题');
            } else if (data.success) {
                Toast.success(data.message);
            } else {
                Toast.error(res.data.message);
            }
            if (Array.isArray(data.warnings) && data.warnings.length > 0) {
                Modal.warning({
                    title: '批量更新警告',
                    content: (
                        <div style={{ maxHeight: 280, overflow: 'auto' }}>
                            {data.warnings.map((msg, idx) => (
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

    // 批量创建渠道
    const batchCreateChannels = async () => {
        if (!currentSupplier || selectedGroupIds.length === 0) {
            Toast.warning('请先选择要创建渠道的分组');
            return;
        }
        
        setBatchCreating(true);
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/batch_create_channels`, {
                group_ids: selectedGroupIds,
            });
            if (res.data.success) {
                Toast.success(res.data.message);
                setSelectedGroupIds([]);
                // 刷新分组列表
                const groupsRes = await API.get(`${API_BASE}/${currentSupplier.id}/groups`);
                if (groupsRes.data.success) {
                    setGroups(groupsRes.data.data || []);
                    // 更新未映射列表
                    const unmapped = (groupsRes.data.data || []).filter(g => !g.local_group);
                    setUnmappedGroups(unmapped.map(g => ({
                        id: g.id,
                        upstream_group: g.upstream_group,
                        group_ratio: g.group_ratio,
                        supported_models: g.supported_models,
                        endpoint_type: g.endpoint_type,
                        has_api_key: !!g.api_key,
                    })));
                }
            } else {
                Toast.error(res.data.message);
            }
            if (Array.isArray(res.data.warnings) && res.data.warnings.length > 0) {
                Modal.info({
                    title: '批量创建结果',
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
        } finally {
            setBatchCreating(false);
        }
    };

    // 批量设置本地分组映射
    const batchMapLocalGroup = async (groupId, localGroup) => {
        try {
            const res = await API.post(`${API_BASE}/${currentSupplier.id}/batch_map_local`, {
                mappings: [{ group_id: groupId, local_group: localGroup }],
            });
            if (res.data.success) {
                Toast.success('映射成功');
                if (res.data.groups) {
                    setGroups(res.data.groups);
                }
                // 更新未映射列表
                const unmapped = (res.data.groups || []).filter(g => !g.local_group);
                setUnmappedGroups(unmapped.map(g => ({
                    id: g.id,
                    upstream_group: g.upstream_group,
                    group_ratio: g.group_ratio,
                    supported_models: g.supported_models,
                    endpoint_type: g.endpoint_type,
                    has_api_key: !!g.api_key,
                })));
            } else {
                Toast.error(res.data.message);
            }
        } catch (e) {
            Toast.error('映射失败');
        }
    };

    // 切换分组选中状态
    const toggleGroupSelection = (groupId) => {
        setSelectedGroupIds(prev => {
            if (prev.includes(groupId)) {
                return prev.filter(id => id !== groupId);
            } else {
                return [...prev, groupId];
            }
        });
    };

    // 全选/取消全选
    const toggleSelectAll = () => {
        if (selectedGroupIds.length === unmappedGroups.length) {
            setSelectedGroupIds([]);
        } else {
            setSelectedGroupIds(unmappedGroups.map(g => g.id));
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
                    <Button size='small' theme='light' onClick={() => openGroups(record)}>
                        分组管理
                    </Button>
                    <Button size='small' theme='light' onClick={() => openEdit(record)}>
                        编辑
                    </Button>
                    <Button size='small' theme='light' onClick={() => checkBalance(record.id)}>
                        查余额
                    </Button>
                    <Popconfirm
                        title='确定删除？'
                        onConfirm={() => deleteSupplier(record.id)}
                    >
                        <Button size='small' type='danger' theme='light'>
                            删除
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    const groupColumns = [
        {
            title: '上游分组',
            dataIndex: 'upstream_group',
            width: 150,
            render: (text) => <Tag color='blue'>{text}</Tag>,
        },
        {
            title: '本地分组',
            dataIndex: 'local_group',
            width: 180,
            render: (text, record) => (
                <Select
                    value={text || undefined}
                    placeholder='选择本地分组'
                    style={{ width: '100%' }}
                    optionList={existingGroups}
                    filter
                    onChange={(value) => {
                        const updated = groups.map(g => 
                            g.id === record.id ? { ...g, local_group: value } : g
                        );
                        setGroups(updated);
                        // 自动保存
                        batchMapLocalGroup(record.id, value);
                    }}
                    allowClear
                />
            ),
        },
        {
            title: '倍率',
            dataIndex: 'group_ratio',
            width: 80,
            render: (text) => <Tag color='cyan'>×{text?.toFixed(3)}</Tag>,
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
        {
            title: 'API密钥',
            dataIndex: 'api_key',
            width: 100,
            render: (text) => (
                <Tag color={text ? 'green' : 'red'}>
                    {text ? '已配置' : '未配置'}
                </Tag>
            ),
        },
    ];

    return (
        <div className='mt-[60px] px-2'>
            <div
                style={{
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    flexWrap: 'wrap',
                    gap: 12,
                }}
            >
                <Space wrap>
                    <Button icon={<IconPlus />} theme='solid' onClick={() => openEdit()}>
                        添加供应商
                    </Button>
                    <Button icon={<IconRefresh />} onClick={loadSuppliers}>
                        刷新
                    </Button>
                </Space>
                <Space wrap style={{ marginLeft: 'auto' }}>
                    <Button
                        theme='solid'
                        type='primary'
                        icon={<IconSync />}
                        loading={syncingAllFull}
                        onClick={syncAllFull}
                    >
                        一键更新所有供应商
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
                width={1000}
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
                            一键更新渠道（推荐）
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
                    <>
                        <Table
                            columns={groupColumns}
                            dataSource={groups}
                            rowKey='id'
                            pagination={false}
                            size='small'
                        />
                        
                        {/* 未映射分组提示 */}
                        {unmappedGroups.length > 0 && (
                            <div style={{ marginTop: 24 }}>
                                <Divider margin='12px'>
                                    <Text type='warning' strong>
                                        未映射分组 ({unmappedGroups.length}个) - 请手动映射后重新执行一键更新
                                    </Text>
                                </Divider>
                                
                                <div style={{ marginBottom: 12, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                                    <Space>
                                        <Checkbox
                                            checked={selectedGroupIds.length === unmappedGroups.length && unmappedGroups.length > 0}
                                            indeterminate={selectedGroupIds.length > 0 && selectedGroupIds.length < unmappedGroups.length}
                                            onChange={toggleSelectAll}
                                        >
                                            全选
                                        </Checkbox>
                                        <Text type='tertiary'>已选择 {selectedGroupIds.length} 个</Text>
                                    </Space>
                                    <Button
                                        type='primary'
                                        theme='solid'
                                        disabled={selectedGroupIds.length === 0}
                                        loading={batchCreating}
                                        onClick={batchCreateChannels}
                                    >
                                        手动创建选中渠道（备用）
                                    </Button>
                                </div>
                                
                                <Table
                                    columns={[
                                        {
                                            title: '选择',
                                            width: 60,
                                            render: (_, record) => (
                                                <Checkbox
                                                    checked={selectedGroupIds.includes(record.id)}
                                                    onChange={() => toggleGroupSelection(record.id)}
                                                />
                                            ),
                                        },
                                        {
                                            title: '上游分组',
                                            dataIndex: 'upstream_group',
                                            width: 150,
                                            render: (text) => <Tag color='orange'>{text}</Tag>,
                                        },
                                        {
                                            title: '映射到本地分组',
                                            width: 200,
                                            render: (_, record) => {
                                                const group = groups.find(g => g.id === record.id);
                                                return (
                                                    <Select
                                                        value={group?.local_group || undefined}
                                                        placeholder='选择本地分组'
                                                        style={{ width: '100%' }}
                                                        optionList={existingGroups}
                                                        filter
                                                        onChange={(value) => {
                                                            batchMapLocalGroup(record.id, value);
                                                        }}
                                                        allowClear
                                                    />
                                                );
                                            },
                                        },
                                        {
                                            title: '倍率',
                                            dataIndex: 'group_ratio',
                                            width: 80,
                                            render: (text) => <Tag color='cyan'>×{text?.toFixed(3)}</Tag>,
                                        },
                                        {
                                            title: '通道类型',
                                            dataIndex: 'endpoint_type',
                                            width: 100,
                                            render: (v) => <Tag>{v || 'openai'}</Tag>,
                                        },
                                        {
                                            title: 'API密钥',
                                            dataIndex: 'has_api_key',
                                            width: 100,
                                            render: (v) => (
                                                <Tag color={v ? 'green' : 'red'}>
                                                    {v ? '已配置' : '未配置'}
                                                </Tag>
                                            ),
                                        },
                                        {
                                            title: '支持模型',
                                            dataIndex: 'supported_models',
                                            render: (v) => {
                                                if (!v) return <Text type='tertiary'>-</Text>;
                                                const models = v.split(',').filter(m => m);
                                                if (models.length === 0) return <Text type='tertiary'>-</Text>;
                                                if (models.length <= 3) return <Text>{models.join(', ')}</Text>;
                                                return <Text>{models.slice(0, 3).join(', ')}... (+{models.length - 3})</Text>;
                                            },
                                        },
                                    ]}
                                    dataSource={unmappedGroups}
                                    rowKey='id'
                                    pagination={false}
                                    size='small'
                                />
                            </div>
                        )}
                    </>
                ) : (
                    <Banner type='info' description='暂无分组数据，请先采集分组或使用一键更新渠道' />
                )}

                {/* 操作说明 */}
                <Collapsible style={{ marginTop: 24 }}>
                    <Collapsible.Header title='操作说明'>
                        <Text>点击展开/收起</Text>
                    </Collapsible.Header>
                    <Collapsible.Body>
                        <div style={{ padding: 12, background: 'var(--semi-color-fill-0)', borderRadius: 8 }}>
                            <Text>
                                <strong>一键更新渠道会自动完成以下操作：</strong><br/>
                                1. 从上游 /api/pricing 采集分组信息（倍率、支持的模型、通道类型）<br/>
                                2. 自动映射本地分组（根据名称规则：cc→cc开头、codex/openai→codex开头、gemini→gemini开头，找倍率最接近的）<br/>
                                3. 为每个分组生成/回填 API 密钥<br/>
                                4. 同步倍率到系统（只同步已映射的本地分组）<br/>
                                5. 自动新增或更新本地渠道（根据映射分组）<br/>
                                6. 硬删除上游已不存在的本地渠道<br/><br/>
                                <strong>重要：未映射分组处理流程</strong><br/>
                                1. 如果上游分组无法自动匹配到本地分组，会显示在「未映射分组」列表中<br/>
                                2. 手动为未映射分组选择对应的本地分组<br/>
                                3. 重新点击「一键更新渠道」完成自动对齐<br/><br/>
                                <strong>建议流程：</strong> 点击「一键更新渠道」→ 检查未映射分组 → 手动映射 → 再次一键更新
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
