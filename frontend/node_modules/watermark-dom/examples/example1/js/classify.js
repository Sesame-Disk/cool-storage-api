var classifyNav = (function(){
	var index,
		$out,
		$box,
		$item,
		widthItem,
		min_move,
		$btnPrev,
		$btnNext;

	function init(){
		index 	   = 0;
		$out 	   = $('.classify .containerH');
		$box 	   = $('.classify .conBox');
		$item 	   = $box.find('.item');
		widthItem  = $item.eq(0).outerWidth(true);
		min_move   = 1 - Math.ceil($item.length / 6);
		$btnPrev   = $('.classify .btnPrev');
		$btnNext   = $('.classify .btnNext');

		var width_box = $item.length * widthItem;
		$box.css('width', width_box)

		$btnPrev.addClass('disabled')
		if(width_box <= $out.width()){
			$btnNext.addClass('disabled')
		}else{
			$btnNext.removeClass('disabled')
		}

		$box.animate({'left': 0}, 400);
	}

	function check(){
		$btnPrev.removeClass('disabled')
		$btnNext.removeClass('disabled')
		
		if(index >= 0){
			index = 0;
			$btnPrev.addClass('disabled')
		}else if(index <= min_move){
			index = min_move;
			$btnNext.addClass('disabled')
		}
	}

	function move(){
		var len = index * widthItem*6;
		$box.animate({'left': len}, 400);
	}

	//init ----------------------------------------
	init();

	$btnPrev.on('click', function(){
		if($(this).hasClass('disabled')) return;

		index++;
		check();
		move();
	})

	$btnNext.on('click', function(){
		if($(this).hasClass('disabled')) return;

		index--;
		check();
		move();
	})

	return {
		init: init
	}

})()

// 每次ajax跟新数据，要执行一下classifyNav.init()