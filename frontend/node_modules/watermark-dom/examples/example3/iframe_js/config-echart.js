/**
 * 画echart图标
 * @param  {object} option 配置对象
 * @param  {string} id     对应id
 */
function initChart(option, id){
	var myChart = echarts.init(document.getElementById(id))
	myChart.setOption(option)
}

/**
 * 发券用券趋势echarts配置
 * @type {Object}
 */
var option_trend ={
	tooltip: {},
	legend: {
		itemGap: 34,
		data: ['发券量', '用券量', '发券金额', '用券金额', '订单量', '销售金额', '付款金额', '买家数', '新买家数'],
		selected: {
			'发券量' : true,
			'用券量' : true,
			'发券金额' : false,
			'用券金额' : false,
			'订单量' : false,
			'销售金额' : false,
			'付款金额' : false,
			'买家数' : false,
			'新买家数' : false
		}
	},
	grid: {
		left: '3%',
		right: '3%',
		top: '15%',
		bottom: '10%'
	},
	xAxis: {
		type: 'category',
		boundaryGap: false,
		axisLine: {
			lineStyle: {
				color: '#e8e8e8'
			}
		},
		axisTick: {
			show: false
		},
		axisLabel: {
			textStyle: {
				color: '#666666'
			}
		},
		splitLine: {
			interval: '16.666',
			lineStyle: {
				color: '#e8e8e8'
			}
		},
		data: ['11-11', '12-12', '12-31']
	},
	yAxis: [
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		},
		{
			type: 'value',
			axisLine: {
				show: false
			},
			axisTick: {
				show: false
			},
			axisLabel: {
				show: false
			},
			splitLine: {
				show: false
			}
		}
	],
	series: [
		{
			name: '发券量',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#75dd30'
				}
			},
			lineStyle: {
				normal: {
					color: '#75dd30'
				}
			},
			yAxisIndex: 0,
			data: [110, 13, 50]
		},
		{
			name: '用券量',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#2d86e1'
				}
			},
			lineStyle: {
				normal: {
					color: '#2d86e1'
				}
			},
			yAxisIndex: 1,
			data: [90, 33, 70]
		},
		{
			name: '发券金额',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#dda831'
				}
			},
			lineStyle: {
				normal: {
					color: '#dda831'
				}
			},
			yAxisIndex: 2,
			data: [130, 55, 10]
		},
		{
			name: '用券金额',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#f27a2a'
				}
			},
			lineStyle: {
				normal: {
					color: '#f27a2a'
				}
			},
			yAxisIndex: 3,
			data: [1, 75, 110]
		},
		{
			name: '订单量',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#932bef'
				}
			},
			lineStyle: {
				normal: {
					color: '#932bef'
				}
			},
			yAxisIndex: 4,
			data: [70, 123, 200]
		},
		{
			name: '销售金额',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#ed2eb7'
				}
			},
			lineStyle: {
				normal: {
					color: '#ed2eb7'
				}
			},
			yAxisIndex: 5,
			data: [300, 0, 60]
		},
		{
			name: '付款金额',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#2fe5ea'
				}
			},
			lineStyle: {
				normal: {
					color: '#2fe5ea'
				}
			},
			yAxisIndex: 6,
			data: [3, 321, 111]
		},
		{
			name: '买家数',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#c8d607'
				}
			},
			lineStyle: {
				normal: {
					color: '#c8d607'
				}
			},
			yAxisIndex: 7,
			data: [323, 155, 22]
		},
		{
			name: '新买家数',
			type: 'line',
			smooth: true,
			itemStyle: {
				normal: {
					color: '#7882f9'
				}
			},
			lineStyle: {
				normal: {
					color: '#7882f9'
				}
			},
			yAxisIndex: 8,
			data: [200, 93, 83]
		}
	]
};


/**
 * 发券用券会员构成echarts配置
 * @type {Object}
 */
var option_member ={
	tooltip: {
		formatter: function(params){
			var show;
			show = '<span class="echarts-point" style="background:'+ params.color +'"></span>' + 
			       params.name +
			       '<br>占比：' + params.percent + '%';
			return show;
		}
	},
	series: [
		{
			name: '会员-发券数量',
			type: 'pie',
			radius: '75px',
			center: ['15%', '45%'],
			minAngle: 8,
			data: [
				{
					name: '未购物会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#2d86e1'
						}
					}
				},
				{
					name: '沉睡会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#08b7b2'
						}
					}
				},
				{
					name: '活跃会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#ff9e12'
						}
					}
				}
			]
		},
		{
			name: '会员-用券数量',
			type: 'pie',
			radius: '75px',
			center: ['50%', '45%'],
			minAngle: 8,
			data: [
				{
					name: '未购物会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#2d86e1'
						}
					}
				},
				{
					name: '沉睡会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#08b7b2'
						}
					}
				},
				{
					name: '活跃会员',
					value: 300,
					itemStyle: {
						normal: {
							color: '#ff9e12'
						}
					}
				}
			]
		},
		{
			name: '买家类型-用券数量',
			type: 'pie',
			radius: '75px',
			center: ['85%', '45%'],
			minAngle: 8,
			data: [
				{
					name: '老买家',
					value: 300,
					itemStyle: {
						normal: {
							color: '#2d86e1'
						}
					}
				},
				{
					name: '新买家',
					value: 300,
					itemStyle: {
						normal: {
							color: '#19c3cc'
						}
					}
				}
			]
		}
	]
};
